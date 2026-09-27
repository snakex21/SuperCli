package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestLightPruningDoesNotRewriteOmittedHistory(t *testing.T) {
	for _, route := range []RouteMode{RouteChatOnly, RouteAdvisor, RouteClarify} {
		t.Run(string(route), func(t *testing.T) {
			l := pruneLoop(t, 1000, 1)
			l.route = route
			bigToolTurn(l, "completed project work", 4, 6000)
			l.Messages = append(l.Messages,
				llm.Message{Role: llm.RoleUser, Content: strings.Repeat("current question ", 300)},
				llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "current", Name: "recall", Arguments: "{}"}}},
				llm.Message{Role: llm.RoleTool, ToolCallID: "current", Name: "recall", Content: "current evidence"})
			before, _ := json.Marshal(l.Messages)
			request := stripStamp(t, l.providerMessages())
			cost := llm.EstimateTokens(request)
			events := make(chan Event, 1)
			gain := l.maybePruneToolResults(context.Background(), events)
			after, _ := json.Marshal(l.Messages)
			if gain != 0 || string(before) != string(after) || len(events) != 0 {
				t.Errorf("pruned history absent from this route: claimed=%d, request=%d -> %d", gain, cost, llm.EstimateTokens(stripStamp(t, l.providerMessages())))
			}
		})
	}
}

func TestLightPruningKeepsArchivedWorkAndReclaimsActiveEvidence(t *testing.T) {
	for _, route := range []RouteMode{RouteChatOnly, RouteAdvisor, RouteClarify} {
		for _, hidden := range []bool{false, true} {
			for _, retrievable := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/hidden=%v/retrievable=%v", route, hidden, retrievable), func(t *testing.T) {
					l, _, _ := resolvedCompactFixture(t, nil, "", 5000)
					if !retrievable {
						l.writer = nil
					}
					l.route, l.pruneProtect = route, 1
					l.Messages = []llm.Message{
						{Role: llm.RoleSystem, Content: "full project policy"},
						{Role: llm.RoleUser, Content: "old visible instruction"},
						{Role: llm.RoleAssistant, Content: "old answer"},
					}
					if hidden {
						if err := l.HideRange(1, 3); err != nil {
							t.Fatal(err)
						}
					}
					for i := 0; i < 6; i++ {
						l.Messages = append(l.Messages,
							llm.Message{Role: llm.RoleUser, Content: strings.Repeat("earlier question ", 100)},
							llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("earlier answer ", 100)})
					}
					bigToolTurn(l, "completed work", 4, 30000)
					current := len(l.Messages)
					l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "active request"})
					for i := 0; i < 4; i++ {
						id := fmt.Sprintf("active-%d", i)
						l.Messages = append(l.Messages,
							llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "recall", Arguments: "{}"}}},
							llm.Message{Role: llm.RoleTool, ToolCallID: id, Name: "recall", Content: strings.Repeat("active evidence ", 400)})
					}
					l.Messages = append(l.Messages,
						llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "latest", Name: "recall", Arguments: "{}"}}},
						llm.Message{Role: llm.RoleTool, ToolCallID: "latest", Name: "recall", Content: "LATEST-EVIDENCE"})
					old, _ := json.Marshal(l.Messages[:current])
					tail, _ := json.Marshal(l.Messages[len(l.Messages)-2:])
					estimate := l.estimateChatRequestTokensRaw()
					if l.chatWindowStart != 0 {
						t.Fatal("estimation mutated the sticky chat window")
					}
					wire := l.providerMessages()
					if estimate != estimateRequestTokens(wire, l.buildToolDefs()) {
						t.Fatal("estimate differs from the prepared request")
					}
					start := l.chatWindowStart
					if start == 0 {
						t.Fatal("fixture did not jump the chat window")
					}
					before := llm.EstimateTokens(stripStamp(t, wire))
					gain := l.maybePruneToolResults(context.Background(), nil)
					if l.chatWindowStart != start {
						t.Fatal("pruning preparation advanced the sticky window")
					}
					after := llm.EstimateTokens(stripStamp(t, l.providerMessages()))
					if gain <= 0 || gain != before-after {
						t.Errorf("wrong active-turn savings: claimed=%d, request=%d -> %d", gain, before, after)
					}
					oldAfter, _ := json.Marshal(l.Messages[:current])
					tailAfter, _ := json.Marshal(l.Messages[len(l.Messages)-2:])
					if string(old) != string(oldAfter) || string(tail) != string(tailAfter) {
						t.Error("changed archived project work or current tool pair")
					}
					assertEvidencePairs(t, l.providerMessages())
				})
			}
		}
	}
}
