package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// This checks the actual manual-compaction entry point, not only the estimator.
// Both policies already remove these blocks from ordinary outgoing requests.
func TestCompactNowDoesNotCountUnsentReasoningAsSavings(t *testing.T) {
	for _, mode := range []string{"discard-completed", "different-provider"} {
		t.Run(mode, func(t *testing.T) {
			var provider llm.Provider = &stubProvider{name: "fixture"}
			if mode == "different-provider" {
				p, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: "http://127.0.0.1:1/v1", Model: "different-model"})
				if err != nil {
					t.Fatal(err)
				}
				provider = p
			}
			reply := policyReply("OLD-NATIVE-STATE")
			reply.Parts[0].Reasoning.Tokens = 100_000
			calls := 0
			l, err := NewLoop(LoopConfig{
				Provider: provider, Registry: tools.NewRegistry(), System: "policy",
				WindowFor: func(string) int { return 50_000 },
				InitialMessages: []llm.Message{
					{Role: llm.RoleUser, Content: "hi"}, reply,
					{Role: llm.RoleUser, Content: "previous correction"},
					{Role: llm.RoleAssistant, Content: "acknowledged"},
					{Role: llm.RoleUser, Content: "current request"},
				},
				Summarizer: func(context.Context, llm.Provider, []llm.Message) (string, error) {
					calls++
					return WrapCompactSummary(strings.Repeat("expanded summary ", 100)), nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			l.discardPreviousReasoning = mode == "discard-completed"
			before, _ := json.Marshal(l.Messages)
			oldEstimate := l.estimateNextRequestTokensRaw()
			if _, err := l.CompactNow(context.Background()); err == nil || !strings.Contains(err.Error(), "insufficient reduction") {
				t.Errorf("accepted apparent savings from unsent state: %v", err)
			}
			after, _ := json.Marshal(l.Messages)
			if string(before) != string(after) {
				t.Errorf("ineffective compaction changed history; estimated request %d -> %d", oldEstimate, l.estimateNextRequestTokensRaw())
			}
			if calls != 1 {
				t.Errorf("summary calls=%d", calls)
			}
		})
	}
}

func TestCompactionBoundaryUsesProjectedRecentTurns(t *testing.T) {
	for _, mode := range []string{"discard-completed", "different-provider", "hidden-tail"} {
		t.Run(mode, func(t *testing.T) {
			var provider llm.Provider = &stubProvider{name: "fixture"}
			if mode == "different-provider" {
				p, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: "http://127.0.0.1:1/v1", Model: "different-model"})
				if err != nil {
					t.Fatal(err)
				}
				provider = p
			}
			recent := policyReply("LARGE-RECENT-REASONING")
			recent.Parts[0].Reasoning.Tokens = 100_000
			if mode == "hidden-tail" {
				recent = llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("HIDDEN-RECENT ", 30_000)}
			}
			var summarized []llm.Message
			l, err := NewLoop(LoopConfig{
				Provider: provider, Registry: tools.NewRegistry(), System: "policy",
				WindowFor: func(string) int { return 5000 },
				InitialMessages: []llm.Message{
					{Role: llm.RoleUser, Content: "older request"},
					{Role: llm.RoleAssistant, Content: strings.Repeat("verified old finding ", 2000)},
					{Role: llm.RoleUser, Content: "previous correction"},
					recent,
					{Role: llm.RoleUser, Content: "current request"},
					{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "live", Name: "read_lines", Arguments: "{\"file\":\"current.go\"}"}}},
					{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "live", Content: "   1 | current evidence"},
				},
				Summarizer: func(_ context.Context, _ llm.Provider, msgs []llm.Message) (string, error) {
					summarized = append([]llm.Message(nil), msgs...)
					return WrapCompactSummary("Done: old finding. Pending: current request."), nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			l.discardPreviousReasoning = mode == "discard-completed"
			if mode == "hidden-tail" {
				if err := l.HideRange(4, 5); err != nil {
					t.Fatal(err)
				}
			}
			tail := append([]llm.Message(nil), l.Messages[3:]...)
			if _, err := l.CompactNow(context.Background()); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(RenderCompactTranscript(summarized), "current request") || strings.Contains(RenderCompactTranscript(summarized), "previous correction") {
				t.Error("unsent recent state forced full-history compaction")
			}
			if len(l.Messages) < len(tail) || !reflect.DeepEqual(l.Messages[len(l.Messages)-len(tail):], tail) {
				t.Error("recent correction or active tool pair no longer intact")
			}
		})
	}
}

func TestCompactionVisibleTurnBoundaryIgnoresHiddenUsers(t *testing.T) {
	l, err := NewLoop(LoopConfig{
		Provider: &stubProvider{name: "fixture"}, Registry: tools.NewRegistry(),
		WindowFor: func(string) int { return 5000 },
		InitialMessages: []llm.Message{
			{Role: llm.RoleUser, Content: "old task"},
			{Role: llm.RoleAssistant, Content: strings.Repeat("old findings ", 3000)},
			{Role: llm.RoleUser, Content: "LAST-VISIBLE-CORRECTION"},
			{Role: llm.RoleAssistant, Content: "visible answer"},
			{Role: llm.RoleUser, Content: "hidden obsolete correction"},
			{Role: llm.RoleAssistant, Content: "hidden answer"},
			{Role: llm.RoleUser, Content: "current task"},
		},
		Summarizer: func(_ context.Context, _ llm.Provider, msgs []llm.Message) (string, error) {
			if strings.Contains(RenderCompactTranscript(msgs), "LAST-VISIBLE-CORRECTION") {
				t.Error("hidden user displaced the last visible correction")
			}
			return WrapCompactSummary("Done: old findings."), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.HideRange(4, 6); err != nil {
		t.Fatal(err)
	}
	if _, err := l.CompactNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range l.VisibleMessages() {
		found = found || m.Content == "LAST-VISIBLE-CORRECTION"
	}
	if !found {
		t.Fatal("last visible correction was replaced by a summary")
	}
}

func TestCompactionProjectionKeepsRequiredNativeReasoning(t *testing.T) {
	const base = "http://127.0.0.1:1/v1"
	scope := sha256.Sum256([]byte(base))
	provider, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: base, Model: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	makeReply := func(marker string) llm.Message {
		reply := policyReply(marker)
		reply.Parts[0].Reasoning.Scope = hex.EncodeToString(scope[:16])
		return reply
	}
	tool := makeReply("required-tool-state")
	tool.ToolCalls = []llm.ToolCall{{ID: "c", Name: "read_lines", Arguments: "{\"file\":\"x.go\"}"}}
	l := &Loop{provider: provider, Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "old task"}, tool,
		{Role: llm.RoleTool, ToolCallID: "c", Name: "read_lines", Content: "   1 | evidence"},
		makeReply("completed-reply-state"),
		{Role: llm.RoleUser, Content: "current task"},
		makeReply("active-state"),
	}}
	before, _ := json.Marshal(l.Messages)
	for _, discard := range []bool{false, true} {
		l.discardPreviousReasoning = discard
		history := l.compactionHistory()
		if !hasPolicyMarker(history.messages, "required-tool-state") || !hasPolicyMarker(history.messages, "active-state") {
			t.Fatal("required/active native state removed")
		}
		if hasPolicyMarker(history.messages, "completed-reply-state") == discard {
			t.Fatal("completed reasoning preference ignored")
		}
		for i := range history.messages {
			if history.originalSplit(i) != i {
				t.Fatal("unhidden boundary changed")
			}
		}
	}
	after, _ := json.Marshal(l.Messages)
	if string(before) != string(after) {
		t.Fatal("compaction projection rewrote native archive")
	}
}

func TestCompactSplitOnlyRelaxesProtectionForLargeVisibleHistory(t *testing.T) {
	for _, tc := range []struct {
		name, older, current string
		want                 int
	}{
		{"two short turns", "done", "current", 1},
		{"oversized previous turn", strings.Repeat("old", 1000), "current", 3},
		{"oversized current turn", "done", strings.Repeat("current", 1000), 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := []llm.Message{
				{Role: llm.RoleSystem, Content: strings.Repeat("retained policy ", 1000)},
				{Role: llm.RoleUser, Content: "previous"},
				{Role: llm.RoleAssistant, Content: tc.older},
				{Role: llm.RoleUser, Content: tc.current},
			}
			if got := compactSplit(messages, 1000); got != tc.want {
				t.Fatalf("split=%d want=%d", got, tc.want)
			}
		})
	}
	one := []llm.Message{{Role: llm.RoleSystem, Content: "policy"}, {Role: llm.RoleUser, Content: strings.Repeat("oversized", 1000)}}
	if got := compactSplit(one, 1000); got != len(one) {
		t.Fatal("one truly oversized turn cannot be compacted")
	}
}
