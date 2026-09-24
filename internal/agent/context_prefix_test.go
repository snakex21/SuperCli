package agent

import (
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestLiveContextKeepsStablePrefixOnEveryRoute(t *testing.T) {
	for _, route := range []RouteMode{RouteCoordinator, RouteChatOnly, RouteAdvisor, RouteClarify} {
		for _, thin := range []bool{false, true} {
			l, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "test"}, Registry: tools.NewRegistry(),
				System: "fixed instructions", LiveContext: "[memory_briefing]\nvalue: previous\n[/memory_briefing]",
				ThinTools: thin, StableToolset: thin, CatalogHoist: thin,
				InitialMessages: []llm.Message{{Role: llm.RoleUser, Content: "original request"}, {Role: llm.RoleAssistant, Content: "verified state"}, {Role: llm.RoleUser, Content: "continue"}}})
			if err != nil {
				t.Fatal(err)
			}
			l.route = route
			before := l.providerMessages()
			l.liveContext = "[memory_briefing]\nvalue: updated\n[/memory_briefing]"
			after := l.providerMessages()
			if len(before) != len(after) || !reflect.DeepEqual(before[:len(before)-1], after[:len(after)-1]) {
				t.Fatalf("route=%v thin=%v changed conversation prefix", route, thin)
			}
			count := 0
			for _, m := range after {
				count += strings.Count(m.Content, "value: updated")
				if strings.Contains(m.Content, "value: previous") {
					t.Fatal("stale memory carried over")
				}
			}
			if count != 1 || !strings.Contains(after[len(after)-1].Content, "value: updated") {
				t.Fatalf("route=%v latest snapshot count=%d", route, count)
			}
			for _, m := range l.AllMessages() {
				if strings.Contains(m.Content, "memory_briefing") {
					t.Fatal("volatile snapshot accumulated in canonical history")
				}
			}
		}
	}
}

func TestLiveContextIncludedInBudgetEveryRoute(t *testing.T) {
	for _, route := range []RouteMode{RouteCoordinator, RouteChatOnly, RouteAdvisor, RouteClarify} {
		l, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "test"}, Registry: tools.NewRegistry(), System: "fixed", InitialMessages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}})
		if err != nil {
			t.Fatal(err)
		}
		l.route = route
		before := l.estimateNextRequestTokensRaw()
		oldTail := l.contextTail()
		l.liveContext = strings.Repeat("updated memory fact. ", 100)
		after := l.estimateNextRequestTokensRaw()
		tailDelta := llm.EstimateMessageTokens(llm.Message{Role: llm.RoleSystem, Content: l.contextTail()}) - llm.EstimateMessageTokens(llm.Message{Role: llm.RoleSystem, Content: oldTail})
		if after-before != tailDelta || tailDelta <= 0 {
			t.Fatalf("route=%v estimate delta=%d tail delta=%d", route, after-before, tailDelta)
		}
	}
}
