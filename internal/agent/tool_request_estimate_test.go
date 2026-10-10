package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestPreparedToolRequestMatchesOwnedSliceAcrossRoutes(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, route := range []RouteMode{RouteCoordinator, RouteChatOnly, RouteAdvisor, RouteClarify} {
			for _, mode := range []string{"plain", "hidden", "native", "discard", "image", "resolved", "retained", "final_only"} {
				t.Run(fmt.Sprintf("thin=%v/%s/%s", thin, route, mode), func(t *testing.T) {
					l := requestPreparationFixture(t, thin, mode)
					l.route = route
					before, _ := json.Marshal(l.Messages)
					for _, phase := range []string{"cold", "warm"} {
						request := l.prepareToolRequest()
						if !reflect.DeepEqual(request.defs, l.buildToolDefsUncached()) || request.tokens != estimateRequestTokens(nil, request.defs) {
							t.Fatalf("%s: captured contracts/estimate differ from actual definitions", phase)
						}
						wire, messageTokens := l.prepareProviderMessages(true)
						if messageTokens+request.tokens != estimateRequestTokens(wire, request.defs) {
							t.Fatalf("%s: full request estimate changed", phase)
						}
					}
					if after, _ := json.Marshal(l.Messages); string(before) != string(after) {
						t.Fatal("request preparation changed canonical history")
					}
				})
			}
		}
	}
}

func TestPreparedToolRequestCompletionUsesCapturedRevision(t *testing.T) {
	l := requestPreparationFixture(t, false, "plain")
	request := l.prepareToolRequest()
	wantDefs := append([]llm.ToolDef(nil), request.defs...)
	l.registry.MustRegister(tools.Tool{Name: "late_contract", Description: strings.Repeat("later contract ", 300), Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	l.registry.MarkAlwaysOn("late_contract")
	if l.toolDefinitionTokens() == request.tokens {
		t.Fatal("fixture did not change the current registry's cost")
	}
	provider := &stubProvider{name: "neutral-provider", scripts: [][]llm.Delta{{
		{Content: "Complete.", FinishReason: "stop", Usage: &llm.Usage{Input: 800, Output: 5, Total: 805}},
	}}}
	l.provider = provider
	if _, _, _, err := l.completeOnce(context.Background(), request, make(chan Event, 32)); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 || !reflect.DeepEqual(provider.toolDefsReqs[0], wantDefs) {
		t.Fatal("completion rebuilt contracts from a later registry or made another request")
	}
	if want := estimateRequestTokens(provider.reqs[0], wantDefs); l.contextBaseEstimated != want || l.contextBaseExact != 800 {
		t.Fatalf("calibration did not match the actual captured request: estimate=%d want=%d exact=%d", l.contextBaseEstimated, want, l.contextBaseExact)
	}
}

func TestPreparedToolRequestOverflowRetryPricesProviderOwnedNormalization(t *testing.T) {
	registry := tools.NewRegistry()
	registry.MustRegister(tools.Tool{Name: "inspect_fixture", Description: "Inspect a fixture", Schema: `{"type":"object"}`, ReadOnly: true,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	registry.MarkAlwaysOn("inspect_fixture")
	calls := 0
	var receivedMessages []llm.Message
	var receivedDefs []llm.ToolDef
	provider := newCompactionCompletionProvider(func(_ context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
		calls++
		if calls == 1 {
			found := false
			for i := range defs {
				if defs[i].Name == "inspect_fixture" {
					defs[i].Description = strings.Repeat("provider-normalized contract ", 400)
					found = true
				}
			}
			if !found {
				return nil, errors.New("fixture definition unavailable")
			}
			return nil, errors.New("context_length_exceeded")
		}
		receivedMessages = append([]llm.Message(nil), messages...)
		receivedDefs = append([]llm.ToolDef(nil), defs...)
		stream := make(chan llm.Delta, 1)
		stream <- llm.Delta{Content: "Complete.", FinishReason: "stop", Usage: &llm.Usage{Input: 900, Output: 5, Total: 905}}
		close(stream)
		return stream, nil
	})
	l, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, System: "Keep the instruction unchanged."})
	if err != nil {
		t.Fatal(err)
	}
	done := false
	for _, event := range drainEvents(t, mustRun(t, l, "Inspect the requested fixture.")) {
		switch event := event.(type) {
		case ErrorEvent:
			t.Fatal(event.Err)
		case DoneEvent:
			done = true
		}
	}
	normalized := false
	for _, def := range receivedDefs {
		if def.Name == "inspect_fixture" && strings.HasPrefix(def.Description, "provider-normalized") {
			normalized = true
		}
	}
	if !done || calls != 2 || !normalized {
		t.Fatal("overflow retry did not retain the provider-owned request")
	}
	if want := estimateRequestTokens(receivedMessages, receivedDefs); l.contextBaseEstimated != want || l.contextBaseExact != 900 {
		t.Fatalf("retry used a stale tool estimate: estimate=%d want=%d exact=%d", l.contextBaseEstimated, want, l.contextBaseExact)
	}
	registered, _ := registry.Get("inspect_fixture")
	if registered.Description != "Inspect a fixture" {
		t.Fatal("provider normalization changed registry contracts")
	}
}

func BenchmarkPreparedToolRequestEstimate(b *testing.B) {
	for _, thin := range []bool{false, true} {
		for _, miss := range []bool{false, true} {
			for _, mode := range []string{"previous", "captured"} {
				b.Run(fmt.Sprintf("thin=%v/miss=%v/%s", thin, miss, mode), func(b *testing.B) {
					l := contextPreparationFixture(b, thin)
					l.prepareToolRequest()
					b.ReportAllocs()
					for b.Loop() {
						if miss {
							l.screenshotForRun = !l.screenshotForRun
						}
						if mode == "previous" {
							defs := l.buildToolDefs()
							preparedRequestEstimateSink = estimateRequestTokens(nil, defs)
						} else {
							request := l.prepareToolRequest()
							preparedRequestEstimateSink = request.tokens
						}
					}
					b.ReportMetric(float64(len(l.prepareToolRequest().defs)), "definitions")
				})
			}
		}
	}
}
