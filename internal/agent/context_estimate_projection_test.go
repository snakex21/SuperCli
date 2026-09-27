package agent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestRequestEstimatePreservesProjectedHistory(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		for _, discard := range []bool{false, true} {
			t.Run(fmt.Sprintf("hidden=%v/discard=%v", hidden, discard), func(t *testing.T) {
				messages := []llm.Message{
					{Role: llm.RoleSystem, Content: "system"},
					{Role: llm.RoleUser, Content: "earlier task"},
					{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "old-read", Name: "read_lines", Arguments: "{}"}}},
					{Role: llm.RoleTool, ToolCallID: "old-read", Name: "read_lines", Content: strings.Repeat("old evidence ", 100)},
					policyReply("completed reasoning"),
					{Role: llm.RoleUser, Content: "current task"},
					{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "current-read", Name: "read_lines", Arguments: "{}"}}, Parts: []llm.ContentPart{{Type: llm.PartTypeReasoning, Reasoning: policyReasoning("active reasoning")}}},
					{Role: llm.RoleTool, ToolCallID: "current-read", Name: "read_lines", Content: "current evidence"},
				}
				l := &Loop{provider: &stubProvider{name: "fixture"}, route: RouteCoordinator, Messages: messages, discardPreviousReasoning: discard}
				expected := append([]llm.Message(nil), messages...)
				if discard {
					expected[4].Parts = []llm.ContentPart{{Type: llm.PartTypeText, Text: "Visible reply."}}
				}
				if hidden {
					if err := l.HideRange(1, 4); err != nil {
						t.Fatal(err)
					}
					expected = append([]llm.Message{expected[0], {Role: llm.RoleUser, Content: "[earlier context cleared — 3 message(s) compacted]"}}, expected[4:]...)
				}
				archiveBefore, _ := json.Marshal(l.Messages)
				assertExact := func(label string) {
					t.Helper()
					// With no tool registry the raw estimate is exactly the projected
					// history cost. Expected messages are stated above, not rebuilt by the
					// production filtering/estimation path.
					want := llm.EstimateTokens(expected)
					for repeat := 0; repeat < 3; repeat++ {
						if got := l.estimateNextRequestTokensRaw(); got != want {
							t.Fatalf("%s: estimate=%d want=%d", label, got, want)
						}
					}
					wire := l.providerMessages()
					// Only the existing request-time freshness stamp follows this history.
					if len(wire) != len(expected)+1 || !reflect.DeepEqual(wire[:len(expected)], expected) {
						t.Fatalf("%s: provider history changed", label)
					}
				}
				assertExact("initial")
				archiveAfter, _ := json.Marshal(l.Messages)
				if string(archiveBefore) != string(archiveAfter) {
					t.Fatal("estimation rewrote the archive")
				}
				tail := llm.Message{Role: llm.RoleAssistant, Content: "new result"}
				l.Messages = append(l.Messages, tail)
				expected = append(expected, tail)
				assertExact("append")
				// Switching the policy back must retain the original native reasoning,
				// even when the raw-history cache was deliberately not warmed before.
				l.discardPreviousReasoning = false
				replyIndex := 4
				if hidden {
					replyIndex = 2
				}
				expected[replyIndex] = messages[4]
				assertExact("restore reasoning")
			})
		}
	}
}

func projectionEstimateFixture(b testing.TB, thin, hidden, discard bool) *Loop {
	b.Helper()
	l := contextPreparationFixture(b, thin)
	for i := range l.Messages {
		if l.Messages[i].Role == llm.RoleAssistant {
			reply := policyReply("unused")
			reply.Parts[0].Reasoning = policyReasoning(strings.Repeat("earlier private reasoning ", 1200))
			reply.Parts[0].Reasoning.Tokens = 0 // older/native state without a token count
			reply.Parts[1].Text = strings.Repeat("visible answer with evidence ", 300)
			l.Messages[i] = reply
		}
	}
	if hidden {
		current := l.Messages
		l.Messages = make([]llm.Message, 1, 2000+len(current))
		l.Messages[0] = current[0]
		for i := 0; i < 2000; i++ {
			l.Messages = append(l.Messages, llm.Message{Role: llm.RoleAssistant, Content: "archived evidence"})
		}
		l.Messages = append(l.Messages, current[1:]...)
		if err := l.HideRange(1, 2001); err != nil {
			b.Fatal(err)
		}
	}
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "continue"})
	l.discardPreviousReasoning = discard
	l.invalidateVisibleEstimate()
	l.EstimateNextRequestTokens()
	return l
}

func BenchmarkContextEstimateProjection(b *testing.B) {
	for _, thin := range []bool{false, true} {
		for _, mode := range []struct {
			name            string
			hidden, discard bool
		}{
			{"plain", false, false}, {"hidden", true, false}, {"projected", false, true}, {"hidden_projected", true, true},
		} {
			b.Run(fmt.Sprintf("thin=%v/%s", thin, mode.name), func(b *testing.B) {
				l := projectionEstimateFixture(b, thin, mode.hidden, mode.discard)
				b.ReportAllocs()
				for b.Loop() {
					l.EstimateNextRequestTokens()
					l.EstimateNextRequestTokens()
					estimateRequestTokens(l.providerMessages(), l.buildToolDefs())
				}
			})
		}
	}
}
