package agent

// Live A/B for catalog placement. It is skipped unless the standard local
// live-test endpoint variables are set. The test uses the same model and tool
// catalog in both arms and reports provider-measured evaluated prompt tokens;
// it never guesses from client-side token estimates.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestCatalogHoist_AB_Live(t *testing.T) {
	baseURL, model := os.Getenv("SUPERCLI_LIVE_BASEURL"), os.Getenv("SUPERCLI_LIVE_MODEL")
	if baseURL == "" || model == "" {
		t.Skip("live A/B: set SUPERCLI_LIVE_BASEURL and SUPERCLI_LIVE_MODEL")
	}
	provider, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: baseURL, Model: model, Timeout: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	runArm := func(t *testing.T, hoist bool) (eval int, calls int) {
		t.Helper()
		reg := tools.NewRegistry()
		noop := func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil }
		for _, name := range thinCoreTools {
			reg.MustRegister(tools.Tool{Name: name, Description: "core " + name, Schema: `{"type":"object"}`, Fn: noop})
			reg.MarkAlwaysOn(name)
		}
		bigSchema := `{"type":"object","properties":{"query":{"type":"string","description":"a deliberately verbose query field for live prompt-cache measurement"},"path":{"type":"string","description":"a deliberately verbose path field for live prompt-cache measurement"},"limit":{"type":"integer","description":"maximum result count"}},"required":["query"]}`
		for i := 0; i < 18; i++ {
			name := fmt.Sprintf("ab_tail_tool_%02d", i)
			reg.MustRegister(tools.Tool{Name: name, Description: "A deterministic dormant catalog tool used only by the catalog-hoist live A/B.", Schema: bigSchema, Fn: noop})
			reg.MarkAlwaysOn(name)
		}
		loop, err := NewLoop(LoopConfig{
			Provider: provider, Registry: reg,
			System:   fmt.Sprintf("Catalog placement A/B arm hoist=%v. Reply with exactly ACK.", hoist),
			MaxSteps: 2, ThinTools: true, StableToolset: true, CatalogHoist: hoist,
		})
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= 3; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			ch, err := loop.Run(ctx, fmt.Sprintf("Round %d: reply ACK only.", i))
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			for event := range ch {
				switch e := event.(type) {
				case DoneEvent:
					// Exclude the cold first request: the question is how much
					// catalog work repeats as the conversation grows.
					if i > 1 {
						eval += e.Usage.Input - e.Usage.Cached
						calls++
					}
				case ErrorEvent:
					cancel()
					t.Fatal(e.Err)
				}
			}
			cancel()
		}
		return eval, calls
	}

	tailEval, tailCalls := runArm(t, false)
	hoistEval, hoistCalls := runArm(t, true)
	t.Logf("catalog A/B: tail eval=%d/%d calls; hoist eval=%d/%d calls; saved=%d evaluated tokens",
		tailEval, tailCalls, hoistEval, hoistCalls, tailEval-hoistEval)
	if tailCalls != 2 || hoistCalls != 2 {
		t.Fatalf("incomplete A/B: tail calls=%d hoist calls=%d", tailCalls, hoistCalls)
	}
	if tailEval > 0 && hoistEval >= tailEval {
		t.Errorf("catalog hoist did not reduce evaluated prompt tokens: tail=%d hoist=%d", tailEval, hoistEval)
	}

	// Quality guard: placement is acceptable only if the model can still
	// notice a direct tail tool and execute it through invoke_tool in both arms.
	qualityArm := func(t *testing.T, hoist bool) {
		t.Helper()
		reg := tools.NewRegistry()
		noop := func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil }
		for _, name := range thinCoreTools {
			if name == invokeToolName {
				continue
			}
			reg.MustRegister(tools.Tool{Name: name, Description: "core " + name, Schema: `{"type":"object"}`, Fn: noop})
			reg.MarkAlwaysOn(name)
		}
		var executed atomic.Bool
		reg.MustRegister(tools.Tool{
			Name: "catalog_probe", Description: "Return the catalog visibility probe token.", ReadOnly: true,
			Schema: `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`,
			Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				executed.Store(true)
				return tools.Result{Text: "CATALOG_PROBE_OK"}, nil
			},
		})
		reg.MarkAlwaysOn("catalog_probe")
		reg.MustRegister(NewInvokeTool(reg).Spec())
		reg.MarkAlwaysOn(invokeToolName)
		loop, err := NewLoop(LoopConfig{
			Provider: provider, Registry: reg, System: "Use requested tools, then answer briefly.",
			MaxSteps: 4, ThinTools: true, StableToolset: true, CatalogHoist: hoist,
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		ch, err := loop.Run(ctx, "Call catalog_probe with query alpha and report its exact result.")
		if err != nil {
			t.Fatal(err)
		}
		for event := range ch {
			if e, ok := event.(ErrorEvent); ok {
				t.Fatal(e.Err)
			}
		}
		if !executed.Load() {
			t.Errorf("hoist=%v: model did not execute catalog_probe", hoist)
		}
	}
	qualityArm(t, false)
	qualityArm(t, true)
}
