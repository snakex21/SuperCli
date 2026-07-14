package agent

// Opt-in local/cloud comparison through the production Loop. It records only
// timing/token counters and prints one JSON object per backend; prompts and
// model output are never written. Configure either or both arms:
//
//   SUPERCLI_EVAL_LOCAL_URL / SUPERCLI_EVAL_LOCAL_MODEL
//   SUPERCLI_EVAL_CLOUD_URL / SUPERCLI_EVAL_CLOUD_MODEL / SUPERCLI_EVAL_CLOUD_KEY

// The ordinary test suite skips this file's test without those variables.

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
)

func TestBackendMatrix_Live(t *testing.T) {
	type arm struct{ name, url, model, key string }
	arms := []arm{
		{"local", os.Getenv("SUPERCLI_EVAL_LOCAL_URL"), os.Getenv("SUPERCLI_EVAL_LOCAL_MODEL"), ""},
		{"cloud", os.Getenv("SUPERCLI_EVAL_CLOUD_URL"), os.Getenv("SUPERCLI_EVAL_CLOUD_MODEL"), os.Getenv("SUPERCLI_EVAL_CLOUD_KEY")},
	}
	ran := 0
	for _, a := range arms {
		if a.url == "" || a.model == "" {
			continue
		}
		ran++
		t.Run(a.name, func(t *testing.T) {
			rec := stats.NewMemory()
			inner, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: a.url, APIKey: a.key, Model: a.model, Timeout: 5 * time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			var p llm.Provider = llm.Metered(inner, a.name, llm.PurposeMain, func(s llm.CallStat) {
				rec.RecordCall(stats.Call{Purpose: s.Purpose, Model: s.Model, Provider: s.Provider,
					TTFTUs: s.TTFT.Microseconds(), DurationUs: s.Duration.Microseconds(),
					TokensIn: s.TokensIn, TokensOut: s.TokensOut, Failed: s.Failed, Canceled: s.Canceled})
			})
			loop, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(),
				System: "Reply briefly and accurately. For the probe token, reply exactly ACK.", MaxSteps: 2, Stats: rec,
				EnableNavigator: true, NavigatorAuto: true, NavigatorKeywordsOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, prompt := range []string{"probe token", "probe token again"} {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				ch, runErr := loop.Run(ctx, prompt)
				if runErr != nil {
					cancel()
					t.Fatal(runErr)
				}
				for ev := range ch {
					if fail, ok := ev.(ErrorEvent); ok {
						cancel()
						t.Fatal(fail.Err)
					}
				}
				cancel()
			}
			report := struct {
				Backend string           `json:"backend"`
				Turns   stats.Total      `json:"turns"`
				Phases  map[string]int64 `json:"phases_us"`
				Calls   []stats.CallAgg  `json:"calls"`
			}{a.name, stats.Sum(rec.Snapshot()), stats.SumPhases(rec.Snapshot()), stats.SumCalls(rec.Calls())}
			b, _ := json.Marshal(report)
			t.Log(string(b))
		})
	}
	if ran == 0 {
		t.Skip("live matrix: set SUPERCLI_EVAL_LOCAL_* and/or SUPERCLI_EVAL_CLOUD_*")
	}
}
