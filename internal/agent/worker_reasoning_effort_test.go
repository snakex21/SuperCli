package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"supercli/internal/llm"
)

// Exercise real wire requests, not only context inspection: sharing a provider
// must not leak the worker preference back into the coordinator.
func TestWorkerReasoningEffortWireIsolation(t *testing.T) {
	previous := llm.ReasoningEffort()
	t.Cleanup(func() { _ = llm.SetReasoningEffort(previous) })
	if err := llm.SetReasoningEffort("low"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ separate, async bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		t.Run(fmt.Sprintf("separate=%v/async=%v", tc.separate, tc.async), func(t *testing.T) {
			efforts := make(chan string, 10)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Effort string `json:"reasoning_effort"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				efforts <- body.Effort
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer srv.Close()
			build := func(model string) llm.Provider {
				p, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: srv.URL, Model: model})
				if err != nil {
					t.Fatal(err)
				}
				return p
			}
			main := build("gpt-5")
			var got []llm.Provider
			at := newModelTestTool(t, &got)
			at.Provider = main
			at.WorkerReasoningEffort = "high"
			if tc.separate {
				at.WorkerProvider = build("gpt-5-mini")
			}
			coordinator := func() {
				ch, err := main.Complete(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hello"}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				for d := range ch {
					if d.Err != nil {
						t.Fatal(d.Err)
					}
				}
				if effort := <-efforts; effort != "low" {
					t.Fatalf("coordinator effort = %q", effort)
				}
			}
			coordinator()
			args, _ := json.Marshal(map[string]any{"prompt": "do it", "async": tc.async})
			result, err := at.execute(context.Background(), args)
			if err != nil || result.Err != nil {
				t.Fatalf("worker: %v / %v", err, result.Err)
			}
			if effort := <-efforts; effort != "high" {
				t.Fatalf("worker effort = %q", effort)
			}
			worker, ok := at.Workers.Get("worker-1")
			if !ok {
				t.Fatal("worker missing")
			}
			worker.runMu.Lock() // join the asynchronous run before continuing it
			worker.runMu.Unlock()
			if _, err := runWorkerLoop(context.Background(), worker, "continue"); err != nil {
				t.Fatal(err)
			}
			if effort := <-efforts; effort != "high" {
				t.Fatalf("continued worker effort = %q", effort)
			}
			coordinator()
			if llm.ReasoningEffort() != "low" {
				t.Fatal("global effort changed")
			}
		})
	}
}
