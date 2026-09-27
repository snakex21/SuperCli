package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestLightCompactionDoesNotCountExcludedProjectTools(t *testing.T) {
	for _, route := range []RouteMode{RouteChatOnly, RouteAdvisor, RouteClarify} {
		for _, mode := range []string{"manual", "auto", "context-limit", "model-switch"} {
			t.Run(fmt.Sprintf("%s/%s", route, mode), func(t *testing.T) {
				messages := []llm.Message{{Role: llm.RoleUser, Content: "old task"}}
				messages = append(messages, completedRead("old", "old.go", strings.Repeat("OLD-VERIFIED-EVIDENCE ", 1500))...)
				messages = append(messages,
					llm.Message{Role: llm.RoleUser, Content: "previous correction"},
					llm.Message{Role: llm.RoleAssistant, Content: "ack"},
					llm.Message{Role: llm.RoleUser, Content: "current question"})
				summary := WrapCompactSummary(strings.Repeat("expanded summary ", 80))
				l, input, calls := resolvedCompactFixture(t, messages, summary, 5000)
				l.route, l.writer = route, nil
				if mode != "manual" {
					l.briefing = strings.Repeat("fixed briefing ", 2500)
				}
				old, _ := json.Marshal(l.Messages)
				before := l.estimateNextRequestTokensRaw()
				l.contextModel = contextModelState{loaded: true, provider: "old", model: "old"}
				switch mode {
				case "manual":
					if _, err := l.CompactNow(context.Background()); err == nil || !strings.Contains(err.Error(), "insufficient reduction") {
						t.Errorf("accepted savings from tool results excluded by the route: %v", err)
					}
				case "auto":
					l.maybeAutoCompact(context.Background(), nil, "")
				case "context-limit":
					l.maybeAutoCompact(context.Background(), nil, "context length exceeded")
				case "model-switch":
					if !l.maybeModelHandoff(context.Background(), nil) {
						t.Fatal("handoff did not trigger")
					}
				}
				after, _ := json.Marshal(l.Messages)
				if string(old) != string(after) {
					t.Errorf("replaced omitted evidence with a larger summary: request %d -> %d", before, l.estimateNextRequestTokensRaw())
				}
				if *calls != 1 || !strings.Contains(RenderCompactTranscript(*input), "OLD-VERIFIED-EVIDENCE") {
					t.Fatal("summary lost authoritative evidence or changed call count")
				}
			})
		}
	}
}

func TestLightCompactionSkipsShortTurnsWithLargeArchivedOutput(t *testing.T) {
	for _, route := range []RouteMode{RouteChatOnly, RouteAdvisor, RouteClarify} {
		t.Run(string(route), func(t *testing.T) {
			messages := []llm.Message{{Role: llm.RoleUser, Content: "earlier task"}}
			messages = append(messages, completedRead("old", "old.go", strings.Repeat("large project output ", 1000))...)
			messages = append(messages, llm.Message{Role: llm.RoleUser, Content: "current question"})
			l, _, calls := resolvedCompactFixture(t, messages, WrapCompactSummary("Done: inspected old.go."), 50000)
			l.route, l.writer = route, nil
			old, _ := json.Marshal(l.Messages)
			if _, err := l.CompactNow(context.Background()); err == nil || !strings.Contains(err.Error(), "nothing to compact") {
				t.Errorf("short outgoing conversation should not need a summary: %v", err)
			}
			after, _ := json.Marshal(l.Messages)
			if *calls != 0 || string(old) != string(after) {
				t.Fatalf("paid %d unnecessary summaries or changed history", *calls)
			}
		})
	}
}

func TestLightCompactionProtectsRecentCorrectionAndActivePair(t *testing.T) {
	for _, route := range []RouteMode{RouteChatOnly, RouteAdvisor, RouteClarify} {
		t.Run(string(route), func(t *testing.T) {
			messages := []llm.Message{
				{Role: llm.RoleUser, Content: "older investigation"},
				{Role: llm.RoleAssistant, Content: strings.Repeat("older verified finding ", 1000)},
				{Role: llm.RoleUser, Content: "PREVIOUS-CORRECTION"},
			}
			messages = append(messages, completedRead("recent", "recent.go", strings.Repeat("recent project output ", 1500))...)
			messages = append(messages,
				llm.Message{Role: llm.RoleUser, Content: "CURRENT-TASK"},
				llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "active", Name: "recall", Arguments: "{}"}}},
				llm.Message{Role: llm.RoleTool, ToolCallID: "active", Name: "recall", Content: "LIVE-EVIDENCE"})
			l, input, calls := resolvedCompactFixture(t, messages, WrapCompactSummary("Done: older findings. Pending: current task."), 5000)
			l.route, l.writer = route, nil
			tail := append([]llm.Message(nil), l.Messages[3:]...)
			before := l.estimateNextRequestTokensRaw()
			if _, err := l.CompactNow(context.Background()); err != nil {
				t.Fatal(err)
			}
			transcript := RenderCompactTranscript(*input)
			if strings.Contains(transcript, "PREVIOUS-CORRECTION") || strings.Contains(transcript, "CURRENT-TASK") {
				t.Error("excluded recent output forced compaction of the active turn")
			}
			if len(l.Messages) < len(tail) || !reflect.DeepEqual(l.Messages[len(l.Messages)-len(tail):], tail) {
				t.Error("recent correction or active tool pair changed")
			}
			if *calls != 1 || l.estimateNextRequestTokensRaw() >= before {
				t.Fatal("useful summary did not reduce the request")
			}
			assertEvidencePairs(t, l.providerMessages())
		})
	}
}

func TestLightCompactionDoesNotSummarizeOutsideStickyWindow(t *testing.T) {
	for _, route := range []RouteMode{RouteChatOnly, RouteAdvisor, RouteClarify} {
		for _, mode := range []string{"manual", "auto", "context-limit", "model-switch"} {
			for _, hidden := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/hidden=%v", route, mode, hidden), func(t *testing.T) {
					var messages []llm.Message
					for i := 0; i < 3; i++ {
						messages = append(messages,
							llm.Message{Role: llm.RoleUser, Content: "old project task"},
							llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("LONG-OLD-EVIDENCE ", 500)})
					}
					// The previous user turn contains multiple assistant messages,
					// e.g. progress/recovery replies before the next real instruction.
					messages = append(messages,
						llm.Message{Role: llm.RoleUser, Content: "previous correction"},
						llm.Message{Role: llm.RoleAssistant, Content: "progress one"},
						llm.Message{Role: llm.RoleAssistant, Content: "progress two"},
						llm.Message{Role: llm.RoleAssistant, Content: "previous final answer"},
						llm.Message{Role: llm.RoleUser, Content: "current question"})
					l, _, calls := resolvedCompactFixture(t, messages, WrapCompactSummary("Done: project task."), 5000)
					l.route, l.writer = route, nil
					if hidden {
						if err := l.HideRange(1, 3); err != nil {
							t.Fatal(err)
						}
					}
					if mode != "manual" {
						l.briefing = strings.Repeat("fixed briefing ", 2500)
					}
					wire := stripStamp(t, l.providerMessages())
					if l.chatWindowStart == 0 || strings.Contains(RenderCompactTranscript(wire), "LONG-OLD-EVIDENCE") {
						t.Fatal("fixture did not exclude the older project history")
					}
					before, _ := json.Marshal(l.Messages)
					oldHidden := append([]bool(nil), l.hidden...)
					start := l.chatWindowStart
					l.contextModel = contextModelState{loaded: true, provider: "old", model: "old"}
					switch mode {
					case "manual":
						if _, err := l.CompactNow(context.Background()); err == nil || !strings.Contains(err.Error(), "nothing to compact") {
							t.Errorf("nothing should be compacted outside the current window: %v", err)
						}
					case "auto":
						l.maybeAutoCompact(context.Background(), nil, "")
					case "context-limit":
						l.maybeAutoCompact(context.Background(), nil, "context length exceeded")
					case "model-switch":
						if l.maybeModelHandoff(context.Background(), nil) {
							t.Error("unnecessary handoff summary")
						}
					}
					after, _ := json.Marshal(l.Messages)
					if *calls != 0 || string(before) != string(after) ||
						!reflect.DeepEqual(oldHidden, l.hidden) || start != l.chatWindowStart {
						t.Fatalf("paid %d summaries or changed excluded history", *calls)
					}
					l.route = RouteCoordinator
					if !strings.Contains(RenderCompactTranscript(l.providerMessages()), "LONG-OLD-EVIDENCE") {
						t.Error("returning to project work lost older evidence")
					}
				})
			}
		}
	}
}

func TestLightCompactionMapsHiddenPrefixAndRetainsTail(t *testing.T) {
	for _, route := range []RouteMode{RouteChatOnly, RouteAdvisor, RouteClarify} {
		t.Run(string(route), func(t *testing.T) {
			messages := []llm.Message{
				{Role: llm.RoleUser, Content: "HIDDEN-REQUEST"},
				{Role: llm.RoleAssistant, Content: strings.Repeat("HIDDEN-EVIDENCE ", 1000)},
				{Role: llm.RoleUser, Content: "visible older task"},
				{Role: llm.RoleAssistant, Content: strings.Repeat("VISIBLE-FINDING ", 1000)},
				{Role: llm.RoleUser, Content: "PREVIOUS-CORRECTION"},
				{Role: llm.RoleAssistant, Content: "ack"},
				{Role: llm.RoleUser, Content: "CURRENT-TASK"},
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "active", Name: "recall", Arguments: "{}"}}},
				{Role: llm.RoleTool, ToolCallID: "active", Name: "recall", Content: "LIVE-EVIDENCE"},
			}
			l, input, calls := resolvedCompactFixture(t, messages, WrapCompactSummary("Done: visible finding."), 5000)
			l.route = route
			if err := l.HideRange(1, 3); err != nil {
				t.Fatal(err)
			}
			tail := append([]llm.Message(nil), l.Messages[5:]...)
			before := l.estimateNextRequestTokensRaw()
			if _, err := l.CompactNow(context.Background()); err != nil {
				t.Fatal(err)
			}
			text := RenderCompactTranscript(*input)
			if *calls != 1 || !strings.Contains(text, "VISIBLE-FINDING") ||
				strings.Contains(text, "HIDDEN-") || strings.Contains(text, "PREVIOUS-CORRECTION") ||
				strings.Contains(text, "LIVE-EVIDENCE") {
				t.Fatal("incorrect summary input mapping")
			}
			if len(l.Messages) < len(tail) || !reflect.DeepEqual(l.Messages[len(l.Messages)-len(tail):], tail) {
				t.Fatal("retained tail changed")
			}
			if l.estimateNextRequestTokensRaw() >= before {
				t.Fatal("request did not shrink")
			}
			assertEvidencePairs(t, l.providerMessages())
		})
	}
}
