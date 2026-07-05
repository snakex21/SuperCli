package agent

// Tests for the light-route (chat-only/advisor/clarify) GROWING history
// window. The old sliding "last 8" tail rewrote the prompt front every
// turn, so the provider KV cache could never reuse more than the leading
// system prompt. The window start must be sticky: below the token
// threshold each request is a strict append to the previous one; past
// the threshold the start jumps forward once, in one big leap.

import (
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func chatLoop(t *testing.T) *Loop {
	t.Helper()
	l, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "stub"}, Registry: tools.NewRegistry(), System: "base"})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	l.route = RouteChatOnly
	return l
}

// stripStamp drops the trailing freshness-stamp system message so two
// requests can be compared for prefix equality.
func stripStamp(t *testing.T, msgs []llm.Message) []llm.Message {
	t.Helper()
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1].Content, "Current local date/time:") {
		t.Fatalf("last message is not the freshness stamp: %+v", msgs)
	}
	return msgs[:len(msgs)-1]
}

// assertPrefix fails unless prev (stamp already stripped) is a literal
// prefix of next by role and content.
func assertPrefix(t *testing.T, prev, next []llm.Message) {
	t.Helper()
	if len(prev) > len(next) {
		t.Fatalf("prev request longer than next: %d > %d", len(prev), len(next))
	}
	for i := range prev {
		if prev[i].Role != next[i].Role || prev[i].Content != next[i].Content {
			t.Fatalf("append-only violated at index %d:\nprev: [%s] %q\nnext: [%s] %q",
				i, prev[i].Role, prev[i].Content, next[i].Role, next[i].Content)
		}
	}
}

// TestChatWindow_AppendOnlyBetweenTurns: below the token threshold,
// request N (minus the trailing stamp) must be a literal prefix of
// request N+1 — the whole point of the growing window. Ten smalltalk
// turns, checked pairwise.
func TestChatWindow_AppendOnlyBetweenTurns(t *testing.T) {
	l := chatLoop(t)
	var prev []llm.Message
	for i := 0; i < 10; i++ {
		l.Messages = append(l.Messages,
			llm.Message{Role: llm.RoleUser, Content: "pytanie numer " + strings.Repeat("x", i+1)},
		)
		cur := stripStamp(t, l.providerMessages())
		if prev != nil {
			assertPrefix(t, prev, cur)
		}
		prev = cur
		l.Messages = append(l.Messages,
			llm.Message{Role: llm.RoleAssistant, Content: "odpowiedź " + strings.Repeat("y", i+1)},
		)
	}
	if l.chatWindowStart != 0 {
		t.Errorf("window start moved below threshold: %d", l.chatWindowStart)
	}
}

// TestChatWindow_JumpOnThreshold: once the history window outgrows
// chatWindowMaxTokens, the start must jump forward IN ONE LEAP, leaving
// exactly chatWindowKeepMsgs eligible history messages — and stay
// append-only again afterwards.
func TestChatWindow_JumpOnThreshold(t *testing.T) {
	l := chatLoop(t)
	// ~500 estimated tokens per message: 12 messages ≈ 6000 > 3000.
	big := strings.Repeat("z", 2000)
	for i := 0; i < 6; i++ {
		l.Messages = append(l.Messages,
			llm.Message{Role: llm.RoleUser, Content: "u" + big},
			llm.Message{Role: llm.RoleAssistant, Content: "a" + big},
		)
	}
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "bieżące pytanie"})
	msgs := stripStamp(t, l.providerMessages())

	if l.chatWindowStart == 0 {
		t.Fatal("window start did not jump past the threshold")
	}
	// Shape: [system] + kept history + current turn (1 user message).
	hist := msgs[1 : len(msgs)-1]
	if len(hist) != chatWindowKeepMsgs {
		t.Fatalf("history after jump = %d messages, want %d", len(hist), chatWindowKeepMsgs)
	}

	// The jump happens ONCE: the next turn appends to the jumped prompt.
	prev := msgs
	startAfterJump := l.chatWindowStart
	l.Messages = append(l.Messages,
		llm.Message{Role: llm.RoleAssistant, Content: "krótka odpowiedź"},
		llm.Message{Role: llm.RoleUser, Content: "kolejne pytanie"},
	)
	next := stripStamp(t, l.providerMessages())
	assertPrefix(t, prev, next)
	if l.chatWindowStart != startAfterJump {
		t.Errorf("window start moved again right after a jump: %d -> %d", startAfterJump, l.chatWindowStart)
	}
}

// TestChatWindow_EligibilityFilterKept: system messages, task
// notifications and tool-call turns must stay out of the history
// window (background agent work must not leak into smalltalk).
func TestChatWindow_EligibilityFilterKept(t *testing.T) {
	l := chatLoop(t)
	l.Messages = append(l.Messages,
		llm.Message{Role: llm.RoleUser, Content: "stare pytanie"},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "tool_search", Arguments: `{}`}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Name: "tool_search", Content: "wynik narzędzia"},
		llm.Message{Role: llm.RoleUser, Content: "<task-notification>worker done</task-notification>"},
		llm.Message{Role: llm.RoleSystem, Content: "reflection checkpoint"},
		llm.Message{Role: llm.RoleUser, Content: "nowe pytanie"},
	)
	msgs := stripStamp(t, l.providerMessages())
	for i, m := range msgs[:len(msgs)-1] { // current turn is the last entry
		if i == 0 {
			continue // leading system prompt
		}
		if m.Role == llm.RoleTool || strings.Contains(m.Content, "<task-notification>") ||
			strings.Contains(m.Content, "reflection checkpoint") || len(m.ToolCalls) > 0 {
			t.Errorf("ineligible message leaked into the chat window at %d: %+v", i, m)
		}
	}
}

// TestChatWindow_ResetOnConversationRebuild: LoadConversation and
// CompactWithSummary invalidate visible indices, so the sticky start
// must reset to 0.
func TestChatWindow_ResetOnConversationRebuild(t *testing.T) {
	l := chatLoop(t)
	l.chatWindowStart = 7
	l.LoadConversation([]llm.Message{{Role: llm.RoleUser, Content: "wznowiona"}})
	if l.chatWindowStart != 0 {
		t.Errorf("LoadConversation must reset chatWindowStart, got %d", l.chatWindowStart)
	}
	l.chatWindowStart = 5
	l.CompactWithSummary("summary")
	if l.chatWindowStart != 0 {
		t.Errorf("CompactWithSummary must reset chatWindowStart, got %d", l.chatWindowStart)
	}
}

// TestChatWindow_ClampWhenViewShrinks: a stale start beyond the current
// visible view (e.g. after /clear collapsed messages) must fall back to
// 0 instead of panicking or silently dropping the whole history.
func TestChatWindow_ClampWhenViewShrinks(t *testing.T) {
	l := chatLoop(t)
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "jedyne pytanie"})
	l.chatWindowStart = 99
	msgs := stripStamp(t, l.providerMessages())
	if len(msgs) < 2 {
		t.Fatalf("request lost the current turn: %+v", msgs)
	}
	if l.chatWindowStart != 0 {
		t.Errorf("stale start not reset: %d", l.chatWindowStart)
	}
}
