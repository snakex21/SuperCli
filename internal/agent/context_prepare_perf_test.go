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

func contextPreparationFixture(t testing.TB, thin bool) *Loop {
	t.Helper()
	reg := tools.NewRegistry()
	noop := func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil }
	for _, name := range thinCoreTools {
		reg.MustRegister(tools.Tool{Name: name, Description: "Core tool " + name, Schema: `{"type":"object","properties":{"query":{"type":"string"}}}`, Fn: noop})
		reg.MarkAlwaysOn(name)
	}
	for i := 0; i < 48; i++ {
		name := fmt.Sprintf("tail_tool_%02d", i)
		reg.MustRegister(tools.Tool{Name: name, Description: "Read structured project information with a bounded range of results.", ReadOnly: true, Schema: `{"type":"object","properties":{"path":{"type":"string"},"query":{"type":"string"},"limit":{"type":"integer","default":100}},"required":["path"]}`, Fn: noop})
		reg.MarkAlwaysOn(name)
	}
	l, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "fixture"}, Registry: reg, System: "Fixture system", ThinTools: thin, StableToolset: true, CatalogHoist: thin})
	if err != nil {
		t.Fatal(err)
	}
	l.route = RouteCoordinator
	for i := 0; i < 40; i++ {
		l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "Inspect project"}, llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("Finding. ", 100)})
	}
	l.providerMessages() // The first request freezes the catalog, as in a live session.
	return l
}

func BenchmarkContextPreparationWarm(b *testing.B) {
	for _, thin := range []bool{false, true} {
		b.Run(fmt.Sprintf("thin=%v", thin), func(b *testing.B) {
			l := contextPreparationFixture(b, thin)
			l.EstimateNextRequestTokens()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				l.EstimateNextRequestTokens() // prune threshold
				l.EstimateNextRequestTokens() // compaction threshold
				defs := l.buildToolDefs()
				estimateRequestTokens(l.providerMessages(), defs)
			}
		})
	}
}

func TestFrozenCatalogEstimateUsesSentCatalog(t *testing.T) {
	l := contextPreparationFixture(t, true)
	before := l.thinToolsPreamble()
	estimateBefore := l.EstimateNextRequestTokens()
	defsBefore := l.buildToolDefs()
	wireBefore := l.providerMessages()[0]
	l.registry.MustRegister(tools.Tool{Name: "late_extension", Description: strings.Repeat("late extension description ", 100), Schema: `{"type":"object","properties":{"file":{"type":"string"}},"required":["file"]}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil }})
	l.registry.Activate("late_extension")
	// A late tail tool does not change the frozen provider catalog or tool defs.
	if !reflect.DeepEqual(defsBefore, l.buildToolDefs()) || !reflect.DeepEqual(wireBefore, l.providerMessages()[0]) {
		t.Fatal("provider prefix changed")
	}
	if got := l.thinToolsPreamble(); got != before {
		t.Fatal("estimator rebuilt a different catalog from the one sent to the provider")
	}
	if got := l.EstimateNextRequestTokens(); got != estimateBefore {
		t.Fatalf("unchanged request estimate grew: %d -> %d", estimateBefore, got)
	}
	// Swapping registries explicitly invalidates the existing frozen catalog.
	replacement := tools.NewRegistry()
	replacement.MustRegister(tools.Tool{Name: "replacement_tool", Description: "New tool", Schema: `{"type":"object"}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil }})
	replacement.MarkAlwaysOn("replacement_tool")
	l.SetRegistry(replacement)
	if got := l.thinToolsPreamble(); !strings.Contains(got, "replacement_tool") || strings.Contains(got, "tail_tool_00") {
		t.Fatalf("stale replacement catalog: %s", got)
	}
	l.route = RouteAdvisor
	if got := l.thinToolsPreamble(); got != "" {
		t.Fatal("catalog leaked into chat route")
	}
	l.route = RouteCoordinator
	l.thinTools = false
	if got := l.thinToolsPreamble(); got != "" {
		t.Fatal("catalog leaked into native mode")
	}
}

func BenchmarkContextPreparationLong(b *testing.B) {
	for _, thin := range []bool{false, true} {
		b.Run(fmt.Sprintf("thin=%v", thin), func(b *testing.B) {
			l := contextPreparationFixture(b, thin)
			for i := range l.Messages {
				if l.Messages[i].Role == llm.RoleAssistant {
					l.Messages[i].Content = strings.Repeat("\tif err != nil { return fmt.Errorf(\"odczyt: %w\", err) }\r\n", 160)
				}
			}
			l.EstimateNextRequestTokens()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				l.EstimateNextRequestTokens()
				l.EstimateNextRequestTokens()
				estimateRequestTokens(l.providerMessages(), l.buildToolDefs())
			}
			b.ReportMetric(float64(llm.EstimateTokens(l.VisibleMessages())), "estimated-history-tokens")
		})
	}
}
