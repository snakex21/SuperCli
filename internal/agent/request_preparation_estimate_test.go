package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func requestPreparationFixture(t testing.TB, thin bool, mode string) *Loop {
	t.Helper()
	l := contextPreparationFixture(t, thin)
	l.Messages = []llm.Message{
		{Role: llm.RoleSystem, Content: "Stable system policy."},
		{Role: llm.RoleUser, Content: "Earlier question."},
		{Role: llm.RoleAssistant, Content: "Earlier answer."},
		{Role: llm.RoleUser, Content: "Current question."},
	}
	l.discardPreviousReasoning = false
	switch mode {
	case "leading_systems":
		l.Messages = append([]llm.Message{l.Messages[0], {Role: llm.RoleSystem},
			{Role: llm.RoleSystem, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "Additional policy in parts."}}}}, l.Messages[1:]...)
	case "hidden":
		if err := l.HideRange(1, 3); err != nil {
			t.Fatal(err)
		}
	case "native", "discard":
		l.Messages[2] = policyReply("original native state")
		l.discardPreviousReasoning = mode == "discard"
	case "image":
		l.Messages[1].Parts = []llm.ContentPart{{Type: llm.PartTypeText, Text: "Caption."},
			{Type: llm.PartTypeImage, Image: &llm.ImageRef{ID: "fixture-image", Name: "fixture.png", Active: true}}}
	case "resolved":
		l.writer = &recordingWriter{}
		if _, ok := l.registry.Get("search_history"); !ok {
			l.registry.MustRegister(tools.Tool{Name: "search_history", Description: "Read archived fixture evidence.", Schema: "{}", ReadOnly: true,
				Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
		}
		exchange := completedRead("archived-read", "old.go", strings.Repeat("old evidence ", 1000))
		l.Messages = append(l.Messages[:2], append(exchange, l.Messages[3])...)
	case "retained":
		l.keepThinking = true
		l.lastThinking = "Explicitly retained reasoning."
	case "final_only":
		l.finalReplyOnly = true
	}
	l.invalidateVisibleEstimate()
	return l
}

func TestPreparedRequestEstimateMatchesExactWire(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, route := range []RouteMode{RouteCoordinator, RouteChatOnly, RouteAdvisor, RouteClarify} {
			for _, mode := range []string{"plain", "leading_systems", "hidden", "native", "discard", "image", "resolved", "retained", "final_only"} {
				t.Run(fmt.Sprintf("thin=%v/%s/%s", thin, route, mode), func(t *testing.T) {
					l := requestPreparationFixture(t, thin, mode)
					l.route = route
					check := func(label string) {
						t.Helper()
						before, _ := json.Marshal(l.Messages)
						defs := l.buildToolDefs()
						wire, tokens := l.prepareProviderMessages(true)
						if got, want := tokens+estimateRequestTokens(nil, defs), estimateRequestTokens(wire, defs); got != want {
							t.Fatalf("%s: prepared estimate=%d; actual wire estimate=%d", label, got, want)
						}
						reference := l.providerMessages()
						// Only normalize independently generated request-time freshness.
						if len(wire) != len(reference) {
							t.Fatalf("%s: wire length changed", label)
						}
						wire[len(wire)-1].Content = "fixture timestamp"
						reference[len(reference)-1].Content = "fixture timestamp"
						if !reflect.DeepEqual(wire, reference) {
							t.Fatalf("%s: wire payload changed", label)
						}
						after, _ := json.Marshal(l.Messages)
						if string(before) != string(after) {
							t.Fatalf("%s: archive changed", label)
						}
					}
					check("cold")
					check("warm")
					l.Messages = append(l.Messages, llm.Message{Role: llm.RoleAssistant, Content: "New appended answer."})
					check("append")
					l.Messages[0].Content = "Updated system policy."
					l.invalidateVisibleEstimate()
					check("edited history")
					if mode == "image" {
						l.Messages[1].Parts[1].Image.Active = false
						check("dormant image")
					}
					if mode == "discard" {
						l.discardPreviousReasoning = false
						check("native state restored")
					}
				})
			}
		}
	}
}

func TestReasoningProjectionUsesLatestCompletedBoundary(t *testing.T) {
	for _, scenario := range []struct {
		name                              string
		firstReply, latestReply, nextUser bool
		removed                           []int
	}{
		{"latest reply", true, true, true, []int{2, 3, 6}},
		{"unfinished tail", true, false, true, []int{2}},
		{"no completed reply", false, false, true, nil},
		{"no next user", true, true, false, nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			nativeOnly := func(marker string) llm.Message {
				return llm.Message{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeReasoning, Reasoning: policyReasoning(marker)}}}
			}
			messages := []llm.Message{
				{Role: llm.RoleSystem, Content: "Policy."}, {Role: llm.RoleUser, Content: "Earlier task."},
				nativeOnly("first"), nativeOnly("between"), nativeOnly("tool-native"),
				{Role: llm.RoleTool, ToolCallID: "call", Name: "read_lines", Content: "Evidence."},
				nativeOnly("latest"), nativeOnly("unfinished"),
			}
			messages[4].ToolCalls = []llm.ToolCall{{ID: "call", Name: "read_lines", Arguments: "{}"}}
			if scenario.firstReply {
				messages[2] = policyReply("first")
			}
			if scenario.latestReply {
				messages[6] = policyReply("latest")
			}
			if scenario.nextUser {
				messages = append(messages, llm.Message{Role: llm.RoleUser, Content: "Current task."}, nativeOnly("active"))
			}
			expected := append([]llm.Message(nil), messages...)
			for _, i := range scenario.removed {
				if i == 2 || i == 6 {
					expected[i].Parts = []llm.ContentPart{{Type: llm.PartTypeText, Text: "Visible reply."}}
				} else {
					expected[i].Parts = []llm.ContentPart{}
					expected[i].Content = noVisibleAnswerPlaceholder
				}
			}
			original, _ := json.Marshal(messages)
			loop := &Loop{discardPreviousReasoning: true}
			if got := loop.reasoningHistoryView(messages); !reflect.DeepEqual(got, expected) {
				t.Fatalf("wrong completed boundary: got=%+v expected=%+v", got, expected)
			}
			after, _ := json.Marshal(messages)
			if string(original) != string(after) {
				t.Fatal("native archive changed")
			}
		})
	}
}

var preparedRequestEstimateSink int
var preparedRequestMessagesSink []llm.Message

// Measure the preparation sequence used around an actual model request:
// prune/compact estimates, tool definitions, wire assembly and final estimate.
// There are no network calls, serialization or model prefill in this benchmark.
func BenchmarkPreparedRequestSequence(b *testing.B) {
	for _, thin := range []bool{false, true} {
		for _, mode := range []string{"content", "parts", "native", "discard", "hidden", "image"} {
			b.Run(fmt.Sprintf("thin=%v/%s", thin, mode), func(b *testing.B) {
				l := contextPreparationFixture(b, thin)
				for i := range l.Messages {
					if l.Messages[i].Role != llm.RoleAssistant {
						continue
					}
					content := strings.Repeat("\tif err != nil { return fmt.Errorf(\"odczyt: %w\", err) }\r\n", 160)
					l.Messages[i].Content = content
					if mode == "parts" {
						l.Messages[i].Content = ""
						l.Messages[i].Parts = []llm.ContentPart{{Type: llm.PartTypeText, Text: content}}
					}
					if mode == "native" || mode == "discard" {
						l.Messages[i].Parts = []llm.ContentPart{{Type: llm.PartTypeReasoning, Reasoning: policyReasoning("historical native state")}}
					}
				}
				l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "Continue."})
				if mode == "hidden" {
					if err := l.HideRange(1, 61); err != nil {
						b.Fatal(err)
					}
				}
				if mode == "image" {
					l.Messages[len(l.Messages)-1].Parts = []llm.ContentPart{{Type: llm.PartTypeImage, Image: &llm.ImageRef{ID: "fixture-image", Active: false}}}
				}
				l.discardPreviousReasoning = mode == "discard"
				l.invalidateVisibleEstimate()
				l.EstimateNextRequestTokens()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					l.EstimateNextRequestTokens()
					l.EstimateNextRequestTokens()
					defs := l.buildToolDefs()
					messages, tokens := l.prepareProviderMessages(true)
					preparedRequestEstimateSink = tokens + estimateRequestTokens(nil, defs)
					preparedRequestMessagesSink = messages
				}
			})
		}
	}
}
