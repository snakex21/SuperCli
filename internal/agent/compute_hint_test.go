package agent

import (
	"context"
	"encoding/json"
	"testing"

	"supercli/internal/tools"
)

func TestComputeHintOnlyForAggregateRequests(t *testing.T) {
	reg := tools.NewRegistry()
	l := &Loop{registry: reg}
	if got := l.computeHint("how many distinct users got a 503?"); got != "" {
		t.Fatal("hint without ctx_execute")
	}
	reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "run", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	for _, p := range []string{
		"How many distinct users got a 503 after the deploy?",
		"List every dependency cycle between the services.",
		"Write a table of all routes sorted by route then method.",
		"Report the p95 latency and the top 5 accounts by total.",
		"Policz ile razy wystąpił błąd w logach.",
	} {
		if l.computeHint(p) != computeHintText {
			t.Errorf("no hint for %q", p)
		}
	}
	for _, p := range []string{"Rename Foo to Bar everywhere.", "Fix the failing test in cart_test.go", "Dodaj flagę --verbose"} {
		if l.computeHint(p) != "" {
			t.Errorf("unexpected hint for %q", p)
		}
	}
}
