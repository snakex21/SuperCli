package agent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// The reference keeps the previous ordering: project media on the read-only
// visible view before selecting a chat window or merging leading system text.
func mediaPreparationReference(l *Loop, tail string) []llm.Message {
	visible := l.mediaProviderView(l.resolvedToolProviderView(llm.ProjectReasoningHistory(l.provider, l.reasoningHistoryView(l.VisibleMessages()))))
	if l.route == RouteCoordinator {
		var out []llm.Message
		if l.stableToolset && l.catalogHoist {
			lead := leadingSystemCount(visible)
			text := make([]string, 0, lead+1)
			for _, msg := range visible[:lead] {
				if s := messageDraftText(msg); s != "" {
					text = append(text, s)
				}
			}
			if l.hoistedPre != "" {
				text = append(text, l.hoistedPre)
			}
			if len(text) > 0 {
				out = append(out, llm.Message{Role: llm.RoleSystem, Content: strings.Join(text, "\n\n")})
			}
			out = append(out, visible[lead:]...)
		} else {
			out = append(out, visible...)
			if pre := l.thinToolsPreamble(); pre != "" {
				out = append(out, llm.Message{Role: llm.RoleSystem, Content: pre})
			}
		}
		return append(out, llm.Message{Role: llm.RoleSystem, Content: tail})
	}
	system := chatOnlySystemPrompt
	if l.route == RouteAdvisor || l.route == RouteClarify {
		system = advisorSystemPrompt
	}
	if l.briefing != "" {
		system += "\n\n" + l.briefing
	}
	window, _, _ := chatHistoryProjection(visible, l.chatWindowStart, false)
	out := append([]llm.Message{{Role: llm.RoleSystem, Content: system}}, window...)
	return append(out, llm.Message{Role: llm.RoleSystem, Content: tail})
}

func TestPreparedMediaPreservesWireAndOwnership(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, stable := range []bool{false, true} {
			for _, hoist := range []bool{false, true} {
				for _, active := range []bool{false, true} {
					for _, place := range []string{"first", "later", "tail", "none"} {
						t.Run(fmt.Sprintf("thin=%v/stable=%v/hoist=%v/active=%v/%s", thin, stable, hoist, active, place), func(t *testing.T) {
							l := contextPreparationFixture(t, thin)
							l.stableToolset, l.catalogHoist, l.discardPreviousReasoning = stable, hoist, false
							img := &llm.ImageRef{ID: "fixture-image", Name: "zrzut ☃ \"image\".png", Active: active}
							block := &llm.ReasoningBlock{Format: llm.ReasoningResponses, Data: json.RawMessage("{\"type\":\"reasoning\",\"encrypted_content\":\"opaque\",\"signature\":\"untouched\"}"), Tokens: 3}
							l.Messages = []llm.Message{
								{Role: llm.RoleSystem, Content: "Policy."},
								{Role: llm.RoleSystem, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "Second policy."}, {Type: llm.PartTypeReasoning, Reasoning: block}}},
								{Role: llm.RoleUser, Content: "Earlier question."},
								{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeReasoning, Reasoning: block}, {Type: llm.PartTypeText, Text: "Earlier answer."}}},
								{Role: llm.RoleUser, Content: "Current question."},
							}
							index := -1
							switch place {
							case "first":
								index = 0
							case "later":
								index = 1
							case "tail":
								index = 4
							}
							if index >= 0 {
								l.Messages[index].Parts = append(l.Messages[index].Parts, llm.ContentPart{Type: llm.PartTypeImage, Image: img}, llm.ContentPart{Type: llm.PartTypeImage})
							}
							l.invalidateVisibleEstimate()
							l.EstimateNextRequestTokens()
							check := func(label string) {
								t.Helper()
								before, _ := json.Marshal(l.Messages)
								start := l.chatWindowStart
								wire, tokens := l.prepareProviderMessages(true)
								nextStart := l.chatWindowStart
								l.chatWindowStart = start
								want := mediaPreparationReference(l, wire[len(wire)-1].Content)
								l.chatWindowStart = nextStart
								if !reflect.DeepEqual(wire, want) {
									t.Fatalf("%s wire changed", label)
								}
								if tokens != llm.EstimateTokens(wire) {
									t.Fatalf("%s prepared estimate differs from exact request", label)
								}
								after, _ := json.Marshal(l.Messages)
								if string(before) != string(after) {
									t.Fatal("archive mutated")
								}
								nativeCount := 0
								for _, msg := range wire {
									for _, part := range msg.Parts {
										if part.Type == llm.PartTypeReasoning {
											nativeCount++
											if part.Reasoning != block {
												t.Fatal("signed native part identity changed")
											}
										}
										if part.Type == llm.PartTypeImage && part.Image != nil && part.Image == img {
											t.Fatal("active image not snapshotted")
										}
									}
								}
								if nativeCount == 0 {
									t.Fatal("native fixture not retained")
								}
								wire[0].Content = "Changed only the owned request."
								after, _ = json.Marshal(l.Messages)
								if string(before) != string(after) {
									t.Fatal("request slice aliases canonical history")
								}
							}
							check("cold")
							check("warm")
							img.Active = !active
							check("image state change")
							l.Messages = append(l.Messages, llm.Message{Role: llm.RoleAssistant, Content: "New appended answer."})
							check("append")
							l.Messages[0].Content = "Updated system policy."
							l.invalidateVisibleEstimate()
							check("edited leading system")
							for _, route := range []RouteMode{RouteChatOnly, RouteAdvisor, RouteClarify} {
								l.route = route
								check(string(route))
							}
						})
					}
				}
			}
		}
	}
}

func TestChatMediaWindowPreservesSharedPricingAndIndices(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, nameSize := range []int{0, 6000} {
			l := contextPreparationFixture(t, true)
			l.route, l.discardPreviousReasoning = RouteChatOnly, false
			l.Messages = nil
			for i := 0; i < 40; i++ {
				l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: strings.Repeat("x", 150)})
			}
			l.Messages[0].Parts = []llm.ContentPart{{Type: llm.PartTypeImage, Image: &llm.ImageRef{ID: "fixture", Name: strings.Repeat("i", nameSize), Active: active}}}
			l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "Current question."})
			l.invalidateVisibleEstimate()
			view, indices, start := chatHistoryProjection(l.Messages, 0, true)
			if start != 0 || len(view) != 41 || len(indices) != 41 {
				t.Fatal("shared estimator/pruning window counted dormant media markers")
			}
			for i, index := range indices {
				if index != i {
					t.Fatal("shared projection indices changed")
				}
			}
			rawCost := llm.EstimateTokens(l.Messages)
			if got := l.estimateChatRequestTokensRaw(); got != rawCost+llm.EstimateMessageTokens(llm.Message{Role: llm.RoleSystem, Content: chatOnlySystemPrompt})+llm.EstimateMessageTokens(llm.Message{Role: llm.RoleSystem, Content: l.contextTail()})+l.toolDefinitionTokens() {
				t.Fatal("shared estimator media cost changed")
			}
			prune := l.pruningHistory()
			if len(prune.requestMessages) != 41 || !reflect.DeepEqual(prune.requestIndices, indices) {
				t.Fatal("pruning view or indices changed")
			}
			wantWindow, _, wantStart := chatHistoryProjection(l.mediaProviderView(l.Messages), 0, false)
			if !active && nameSize == 6000 && wantStart == 0 {
				t.Fatal("fixture did not cross the request-only media threshold")
			}
			got, tokens := l.prepareProviderMessages(true)
			if l.chatWindowStart != wantStart || !reflect.DeepEqual(got[1:len(got)-1], wantWindow) {
				t.Fatal("provider media window changed")
			}
			if tokens != llm.EstimateTokens(got) {
				t.Fatal("provider image markers not priced")
			}
			prune = l.pruningHistory()
			if prune.requestOriginalIndex(0) != wantStart {
				t.Fatal("post-request pruning start changed")
			}
		}
	}
}

func TestPreparedImageSnapshotSurvivesDeactivation(t *testing.T) {
	for _, route := range []RouteMode{RouteCoordinator, RouteChatOnly, RouteAdvisor, RouteClarify} {
		l := contextPreparationFixture(t, true)
		l.route = route
		img := &llm.ImageRef{ID: "fixture", Path: "fixture.png", Data: "not-read", Active: true}
		l.Messages = []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "Caption."}, {Type: llm.PartTypeImage, Image: img}, {Type: llm.PartTypeImage}}}}
		l.invalidateVisibleEstimate()
		wire, _ := l.prepareProviderMessages(true)
		l.deactivateActiveImages()
		found := false
		for _, msg := range wire {
			for _, part := range msg.Parts {
				if part.Image != nil {
					found = true
					if !part.Image.Active || part.Image.Data != "not-read" || part.Image == img {
						t.Fatal("request snapshot changed when live refs deactivated")
					}
				}
			}
		}
		if !found || img.Active || img.Data != "" {
			t.Fatal("fixture did not exercise accepted-request deactivation")
		}
	}
}

// The private saved-session replay establishes incidence and before/after
// evidence. This fixture stays reproducible without user data.
func BenchmarkPreparedMediaProjection(b *testing.B) {
	for _, route := range []RouteMode{RouteCoordinator, RouteChatOnly} {
		for _, images := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/images=%v", route, images), func(b *testing.B) {
				l := contextPreparationFixture(b, true)
				l.route = route
				l.Messages = []llm.Message{{Role: llm.RoleSystem, Content: "Stable policy."}}
				for i := 0; i < 929; i++ {
					l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "Inspect project."}, llm.Message{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: strings.Repeat("Bounded saved evidence. ", 16)}}})
				}
				if images {
					for _, index := range []int{1, 31} {
						l.Messages[index].Parts = append(l.Messages[index].Parts, llm.ContentPart{Type: llm.PartTypeImage, Image: &llm.ImageRef{ID: "fixture", Name: "fixture.png"}})
					}
				}
				l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "Continue."})
				l.invalidateVisibleEstimate()
				l.EstimateNextRequestTokens()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					l.EstimateNextRequestTokens()
					l.EstimateNextRequestTokens()
					defs := l.buildToolDefs()
					msgs, tokens := l.prepareProviderMessages(true)
					preparedRequestMessagesSink = msgs
					preparedRequestEstimateSink = tokens + estimateRequestTokens(nil, defs)
				}
			})
		}
	}
}
