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
	"strings"
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
	// Thinking-capable local models can legitimately spend several minutes on
	// the quality arms. The cache arms disable thinking below, but the shared
	// client timeout must still accommodate the representative quality check.
	provider, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: baseURL, Model: model, Timeout: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	// Catalog placement measures prompt evaluation, not reasoning quality.
	// Qwen's soft switch avoids spending minutes generating a hidden chain of
	// thought for an exact one-token ACK while leaving the prompt/catalog/cache
	// layout under test unchanged in both arms.
	cacheDirective := ""
	if strings.Contains(strings.ToLower(model), "qwen") {
		cacheDirective = "\n/no_think"
	}

	runArm := func(t *testing.T, hoist bool) (eval int, cached int, calls int) {
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
			System:   fmt.Sprintf("Catalog placement A/B arm hoist=%v. Reply with exactly ACK.%s", hoist, cacheDirective),
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
					t.Logf("catalog A/B arm hoist=%v round=%d input=%d cached=%d eval=%d",
						hoist, i, e.Usage.Input, e.Usage.Cached, e.Usage.Input-e.Usage.Cached)
					// Exclude the cold first request: the question is how much
					// catalog work repeats as the conversation grows.
					if i > 1 {
						eval += e.Usage.Input - e.Usage.Cached
						cached += e.Usage.Cached
						calls++
					}
				case ErrorEvent:
					cancel()
					t.Fatal(e.Err)
				}
			}
			cancel()
		}
		return eval, cached, calls
	}

	tailEval, tailCached, tailCalls := runArm(t, false)
	hoistEval, hoistCached, hoistCalls := runArm(t, true)
	t.Logf("catalog A/B: tail eval=%d cached=%d/%d calls; hoist eval=%d cached=%d/%d calls; saved=%d evaluated tokens",
		tailEval, tailCached, tailCalls, hoistEval, hoistCached, hoistCalls, tailEval-hoistEval)
	if tailCalls != 2 || hoistCalls != 2 {
		t.Fatalf("incomplete A/B: tail calls=%d hoist calls=%d", tailCalls, hoistCalls)
	}
	if tailCached+hoistCached == 0 {
		t.Log("catalog A/B is cache-inconclusive: provider reported zero cached tokens in both arms")
	} else if tailEval > 0 && hoistEval >= tailEval {
		t.Errorf("catalog hoist did not reduce evaluated prompt tokens: tail=%d hoist=%d", tailEval, hoistEval)
	}

	// Quality guard runs with normal thinking enabled. Both arms must discover
	// and execute the tail tool without tool errors. Call count and max-steps
	// are logged but deliberately not compared: this Qwen architecture has a
	// known stochastic tool-loop tendency independent of catalog placement.
	type qualityResult struct {
		calls, errors int
		names         []string
		executed      bool
		runErr        string
	}
	qualityArm := func(t *testing.T, hoist bool) qualityResult {
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
			// Deliberately NO /no_think here. Placement is accepted only if
			// the model discovers and calls the tail tool in its normal,
			// quality-oriented reasoning mode.
			Provider: provider, Registry: reg, System: "Use requested tools, then answer briefly.",
			MaxSteps: 4, ThinTools: true, StableToolset: true, CatalogHoist: hoist,
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		ch, err := loop.Run(ctx, "Call catalog_probe with query alpha and report its exact result.")
		if err != nil {
			t.Fatal(err)
		}
		result := qualityResult{}
		for event := range ch {
			switch e := event.(type) {
			case ToolCallEvent:
				result.calls++
				result.names = append(result.names, e.Name)
			case ToolResultEvent:
				if e.Err != nil {
					result.errors++
					t.Logf("hoist=%v: tool error: %v", hoist, e.Err)
				}
			case ErrorEvent:
				result.runErr = e.Err.Error()
			}
		}
		result.executed = executed.Load()
		t.Logf("catalog quality thinking=true hoist=%v executed=%v calls=%d errors=%d run_err=%q names=%v",
			hoist, result.executed, result.calls, result.errors, result.runErr, result.names)
		return result
	}
	tailQuality := qualityArm(t, false)
	hoistQuality := qualityArm(t, true)
	if !tailQuality.executed || tailQuality.errors != 0 {
		t.Errorf("tail quality failed: %+v", tailQuality)
	}
	if !hoistQuality.executed || hoistQuality.errors != 0 {
		t.Errorf("hoist quality failed: %+v", hoistQuality)
	}
}
