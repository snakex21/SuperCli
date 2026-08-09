package agent

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestStripThinking_RemovesBlocks(t *testing.T) {
	cases := []struct{ in, want string }{
		{"<thinking>plan</thinking>answer", "answer"},
		{"a <think>x</think> b", "a  b"},
		{"<reasoning>blah</reasoning>final", "final"},
		{"no tags here", "no tags here"},
		{"before\n<thinking>\nl1\nl2\n</thinking>\nafter", "before\n\nafter"},
		{"<thinking>truncated stream never closed", ""},
	}
	for _, c := range cases {
		if got := stripThinking(c.in); got != c.want {
			t.Errorf("stripThinking(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestStripThinkingFromMessage_DropsEmptiedTextPart guards the tool-call
// case: a turn whose only text is a <thinking> block (model reasoned,
// then emitted a tool call with no visible answer) must NOT leave an
// empty text part, which the provider rejects on the next request. The
// tool call is preserved.
func TestStripThinkingFromMessage_DropsEmptiedTextPart(t *testing.T) {
	msg := llm.Message{
		Role:  llm.RoleAssistant,
		Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "<thinking>which tool?</thinking>"}},
		ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "ctx_execute", Arguments: `{"command":["ls"]}`},
		},
	}
	got := stripThinkingFromMessage(msg)
	for i, p := range got.Parts {
		if p.Type == llm.PartTypeText && p.Text == "" {
			t.Errorf("part %d is an empty text part; want it dropped", i)
		}
	}
	if len(got.ToolCalls) != 1 {
		t.Fatalf("tool call lost: %+v", got.ToolCalls)
	}
}

func TestStripThinkingFromMessage_ReasoningOnlyStaysProviderValid(t *testing.T) {
	msg := llm.Message{
		Role:  llm.RoleAssistant,
		Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "<thinking>private reasoning only</thinking>"}},
	}
	got := stripThinkingFromMessage(msg)
	if err := got.Validate(); err != nil {
		t.Fatalf("reasoning-only assistant became invalid: %+v: %v", got, err)
	}
	if got.Content != "[no visible answer]" {
		t.Fatalf("placeholder = %q", got.Content)
	}
	if strings.Contains(got.Content, "private reasoning") {
		t.Fatal("reasoning leaked into provider history")
	}
}

// TestLoop_HistoryStripsThinkingButStorageKeepsIt proves the Task 2b
// invariant: the in-memory history that drives the next request carries
// only the final answer, while the session store keeps the full text
// (with <thinking>) for UI replay.
func TestLoop_HistoryStripsThinkingButStorageKeepsIt(t *testing.T) {
	prov := makeScriptedProvider("<thinking>let me reason at length</thinking>\nThe answer is 42.")
	reg := emptyRegistry()
	w := &recordingWriter{}
	loop, err := NewLoop(LoopConfig{Provider: prov, Registry: reg, Writer: w})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	events, err := loop.Run(context.Background(), "what is 6*7?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for range events {
	}

	// In-memory history: last message is the assistant answer, stripped.
	var lastAssistant *llm.Message
	for i := range loop.Messages {
		if loop.Messages[i].Role == llm.RoleAssistant {
			lastAssistant = &loop.Messages[i]
		}
	}
	if lastAssistant == nil {
		t.Fatal("no assistant message in history")
	}
	histText := lastAssistant.Content
	for _, p := range lastAssistant.Parts {
		histText += p.Text
	}
	if strings.Contains(histText, "<thinking>") || strings.Contains(histText, "let me reason") {
		t.Errorf("history assistant message still carries thinking: %q", histText)
	}
	if !strings.Contains(histText, "The answer is 42.") {
		t.Errorf("history assistant message lost its answer: %q", histText)
	}

	// Session store: full text with thinking preserved for the UI.
	var storedAssistant *llm.Message
	for i := range w.messages {
		if w.messages[i].Role == llm.RoleAssistant {
			storedAssistant = &w.messages[i]
		}
	}
	if storedAssistant == nil {
		t.Fatal("no assistant message persisted")
	}
	storeText := storedAssistant.Content
	for _, p := range storedAssistant.Parts {
		storeText += p.Text
	}
	if !strings.Contains(storeText, "<thinking>") || !strings.Contains(storeText, "let me reason") {
		t.Errorf("stored assistant message must keep thinking for UI, got: %q", storeText)
	}
}

// TestLoadConversation_StripsThinking proves resumed assistant turns are
// stripped so the live prefix stays consistent with fresh turns.
func TestLoadConversation_StripsThinking(t *testing.T) {
	prov := makeScriptedProvider("x")
	loop, _ := NewLoop(LoopConfig{Provider: prov, Registry: emptyRegistry()})
	loop.LoadConversation([]llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Content: "<thinking>secret</thinking>visible reply"},
	})
	for _, m := range loop.Messages {
		if m.Role == llm.RoleAssistant && strings.Contains(m.Content, "secret") {
			t.Errorf("resumed assistant message still carries thinking: %q", m.Content)
		}
	}
}

// reqText flattens a wire request to text for assertions.
func reqText(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
		for _, p := range m.Parts {
			b.WriteString(p.Text)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// --- reasoning retention (SUPERCLI_KEEP_THINKING) ---

func TestCaptureThinking_ClosedBlock(t *testing.T) {
	plain, captured := captureThinking("a<thinking>plan</thinking>b")
	if plain != "ab" {
		t.Errorf("plain = %q, want %q", plain, "ab")
	}
	if captured != "<thinking>plan</thinking>" {
		t.Errorf("captured = %q", captured)
	}
}

func TestCaptureThinking_TruncatedBlock(t *testing.T) {
	plain, captured := captureThinking("answer first<thinking>cut off")
	if plain != "answer first" {
		t.Errorf("plain = %q", plain)
	}
	if !strings.HasPrefix(captured, "<thinking>") || !strings.Contains(captured, "cut off") {
		t.Errorf("captured = %q", captured)
	}
}

func TestCaptureThinking_NoMarkersIsFastPath(t *testing.T) {
	plain, captured := captureThinking("ordinary text")
	if plain != "ordinary text" || captured != "" {
		t.Errorf("plain=%q captured=%q, want unchanged/empty", plain, captured)
	}
}

func TestCaptureThinkingFromMessage_AcrossContentAndParts(t *testing.T) {
	msg := llm.Message{
		Role: llm.RoleAssistant,
		Content: "first <thinking>one</thinking> middle",
		Parts: []llm.ContentPart{
			{Type: llm.PartTypeText, Text: "<reasoning>two</reasoning>tail"},
			{Type: llm.PartTypeImage, Image: &llm.ImageRef{Data: "AAA", MediaType: "image/png"}},
		},
	}
	captured, plain := captureThinkingFromMessage(msg)
	if !strings.Contains(captured, "one") || !strings.Contains(captured, "two") {
		t.Errorf("captured = %q, want both blocks", captured)
	}
	if strings.Contains(plain.Content, "one") || strings.Contains(plain.Content, "two") {
		t.Errorf("plain still carries thinking: %+v", plain)
	}
	if len(plain.Parts) != 2 {
		t.Errorf("parts = %d, want 2 (text tail + image)", len(plain.Parts))
	}
	if plain.Parts[1].Type != llm.PartTypeImage {
		t.Errorf("image part lost: %+v", plain.Parts)
	}
}

// providerMessages must append the retained thinking at the very tail
// (with the freshness stamp) on BOTH routes, and only when retention
// is enabled and thinking exists.
func TestProviderMessages_RetainedThinking(t *testing.T) {
	for _, route := range []RouteMode{RouteCoordinator, RouteChatOnly} {
		t.Run(string(route), func(t *testing.T) {
			l := &Loop{
				route:        route,
				keepThinking: true,
				lastThinking: "<thinking>previous plan</thinking>",
				Messages: []llm.Message{
					{Role: llm.RoleSystem, Content: "base"},
					{Role: llm.RoleUser, Content: "current prompt"},
				},
			}
			msgs := l.providerMessages()
			last := msgs[len(msgs)-1]
			if !strings.Contains(last.Content, "Retained reasoning") || !strings.Contains(last.Content, "previous plan") {
				t.Fatalf("last message lost retained thinking: %q", last.Content)
			}
			if strings.Contains(last.Content, "current prompt") {
				t.Errorf("thinking must ride at the tail, not inside the user message: %q", last.Content)
			}

			// Retention off: no thinking.
			l.keepThinking = false
			if last2 := l.providerMessages()[len(msgs)-1]; strings.Contains(last2.Content, "Retained reasoning") {
				t.Errorf("retention off still injects thinking: %q", last2.Content)
			}

			// No thinking yet: no marker.
			l.keepThinking = true
			l.lastThinking = ""
			if last3 := l.providerMessages()[len(msgs)-1]; strings.Contains(last3.Content, "Retained reasoning") {
				t.Errorf("empty thinking still injected: %q", last3.Content)
			}
		})
	}
}

// The coordinator route must keep valid role alternation: retained
// thinking never becomes a standalone message.
func TestProviderMessages_RetainedThinkingNeverBreaksRoles(t *testing.T) {
	l := &Loop{
		route:        RouteCoordinator,
		keepThinking: true,
		lastThinking: "<thinking>plan</thinking>",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "base"},
			{Role: llm.RoleUser, Content: "hi"},
		},
	}
	msgs := l.providerMessages()
	for i, m := range msgs {
		if err := m.Validate(); err != nil {
			t.Fatalf("message %d invalid: %v", i, err)
		}
	}
	if got := msgs[len(msgs)-1].Role; got != llm.RoleSystem {
		t.Errorf("tail role = %v, want system", got)
	}
}

// /clear must drop stale retained reasoning together with the turns
// it came from.
func TestHideLastUserTurns_ClearsRetainedThinking(t *testing.T) {
	l := &Loop{
		keepThinking: true,
		lastThinking: "<thinking>old turn</thinking>",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "old"},
			{Role: llm.RoleAssistant, Content: "old answer"},
			{Role: llm.RoleUser, Content: "current"},
		},
	}
	if hidden := l.HideLastUserTurns(1); hidden == 0 {
		t.Fatal("expected /clear to hide the old turn")
	}
	if l.lastThinking != "" {
		t.Errorf("lastThinking = %q after /clear, want empty", l.lastThinking)
	}
}

// /resume restores the last reasoning as the first request's tail.
func TestLoadConversation_RestoresRetainedThinking(t *testing.T) {
	l := &Loop{
		keepThinking: true,
		Messages:     []llm.Message{{Role: llm.RoleSystem, Content: "base"}},
	}
	l.LoadConversation([]llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Content: "<thinking>resumed reasoning</thinking>visible"},
	})
	if l.lastThinking == "" || !strings.Contains(l.lastThinking, "resumed reasoning") {
		t.Errorf("lastThinking = %q after resume, want resumed reasoning", l.lastThinking)
	}
	msgs := l.providerMessages()
	if !strings.Contains(msgs[len(msgs)-1].Content, "resumed reasoning") {
		t.Errorf("resumed thinking missing from first request: %q", msgs[len(msgs)-1].Content)
	}
}

func TestKeepThinkingEnabled_EnvParsing(t *testing.T) {
	for _, v := range []string{"0", "false", "no", "off", "OFF"} {
		t.Setenv("SUPERCLI_KEEP_THINKING", v)
		if keepThinkingEnabled() {
			t.Errorf("SUPERCLI_KEEP_THINKING=%s: enabled, want disabled", v)
		}
	}
	t.Setenv("SUPERCLI_KEEP_THINKING", "1")
	if !keepThinkingEnabled() {
		t.Error("SUPERCLI_KEEP_THINKING=1: disabled, want enabled")
	}
	t.Setenv("SUPERCLI_KEEP_THINKING", "")
	if !keepThinkingEnabled() {
		t.Error("unset: disabled, want enabled (default on)")
	}
}

// End to end through a real Run: the thinking of turn 1 must arrive in
// the provider request of turn 2, and the assistant history must stay
// clean on both turns.
func TestLoop_RetainedThinkingSurvivesNextRun(t *testing.T) {
	prov := &stubProvider{name: "retain"}
	reg := emptyRegistry()
	l, err := NewLoop(LoopConfig{Provider: prov, Registry: reg, KeepThinking: true})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	prov.scripts = [][]llm.Delta{
		{{Content: "<thinking>secret reasoning</thinking>visible answer", FinishReason: "stop", Usage: &llm.Usage{Total: 1}}},
		{{Content: "final", FinishReason: "stop", Usage: &llm.Usage{Total: 1}}},
	}
	if events, err := l.Run(context.Background(), "first task"); err != nil {
		t.Fatalf("Run 1: %v", err)
	} else {
		for range events {
		}
	}
	if events, err := l.Run(context.Background(), "second task"); err != nil {
		t.Fatalf("Run 2: %v", err)
	} else {
		for range events {
		}
	}

	reqs := prov.reqs
	if len(reqs) < 2 {
		t.Fatalf("got %d provider calls, want >= 2", len(reqs))
	}
	req2 := reqText(reqs[1])
	if !strings.Contains(req2, "secret reasoning") {
		t.Errorf("turn 2 request lost retained thinking:\n%s", req2)
	}
	if strings.Contains(req2, "Retained reasoning") == false {
		t.Errorf("turn 2 request lacks the retention marker")
	}
	// The history view itself must never contain the thinking.
	for _, m := range l.Messages {
		if m.Role == llm.RoleAssistant && strings.Contains(m.Content, "secret reasoning") {
			t.Errorf("thinking leaked into in-memory history: %q", m.Content)
		}
	}
}
