package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"supercli/internal/llm"
)

func purposeWireProvider(t *testing.T, reply string) (llm.Provider, <-chan string) {
	t.Helper()
	efforts := make(chan string, 20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Effort string `json:"reasoning_effort"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		efforts <- body.Effort
		payload, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": reply}, "finish_reason": "stop"}}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
	}))
	t.Cleanup(srv.Close)
	p, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: srv.URL, Model: "gpt-5"})
	if err != nil {
		t.Fatal(err)
	}
	return p, efforts
}

func TestAdvisorPurposeEffortWireAndContinuation(t *testing.T) {
	initial := llm.ReasoningEffort()
	t.Cleanup(func() { _ = llm.SetReasoningEffort(initial) })
	_ = llm.SetReasoningEffort("medium")
	for _, separate := range []bool{false, true} {
		for _, async := range []bool{false, true} {
			for _, level := range []string{"", "high"} {
				t.Run(fmt.Sprintf("separate=%v/async=%v/level=%s", separate, async, level), func(t *testing.T) {
					p, efforts := purposeWireProvider(t, "done")
					var providers []llm.Provider
					at := newModelTestTool(t, &providers)
					at.Provider = p
					at.WorkerReasoningEffort = "low"
					at.AdvisorReasoningEffort = level
					if separate {
						at.AdvisorProvider = p
					}
					ctx, cancel := context.WithTimeout(llm.WithReasoningEffort(context.Background(), "max"), 10*time.Second)
					defer cancel()
					ar, _ := json.Marshal(agentArgs{Prompt: "give a second opinion", Advise: true, Async: async})
					res, err := at.execute(ctx, ar)
					if err != nil || res.Err != nil {
						t.Fatalf("advisor: %v %v", err, res.Err)
					}
					want := level
					if want == "" {
						want = "medium"
					}
					select {
					case got := <-efforts:
						if got != want {
							t.Fatalf("advisor effort=%q want=%q", got, want)
						}
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					w, ok := at.Workers.Get("worker-1")
					if !ok {
						t.Fatal("advisor missing")
					}
					w.runMu.Lock() // join the asynchronous run before continuing
					w.runMu.Unlock()
					if _, err := runWorkerLoop(ctx, w, "follow up"); err != nil {
						t.Fatal(err)
					}
					if got := <-efforts; got != want {
						t.Fatalf("continued advisor effort=%q want=%q", got, want)
					}
					if w.Snapshot().ReasoningEffort != want {
						t.Fatalf("advisor badge: %+v", w.Snapshot())
					}
					if llm.ReasoningEffort() != "medium" || llm.ReasoningEffort(ctx) != "max" {
						t.Fatal("effort leaked")
					}
				})
			}
		}
	}
}

func TestPostReviewPurposeEffortWireIsolation(t *testing.T) {
	initial := llm.ReasoningEffort()
	t.Cleanup(func() { _ = llm.SetReasoningEffort(initial) })
	_ = llm.SetReasoningEffort("medium")
	for _, level := range []string{"", "high"} {
		t.Run("level="+level, func(t *testing.T) {
			p, efforts := purposeWireProvider(t, `{"decision":"accept"}`)
			l, _, _ := postReviewLoop(t, false, &PostReviewConfig{Provider: p, ReasoningEffort: level})
			runPostReviewLoop(t, l)
			want := level
			if want == "" {
				want = "medium"
			}
			select {
			case got := <-efforts:
				if got != want {
					t.Fatalf("review effort=%q want=%q", got, want)
				}
			default:
				t.Fatal("review request missing")
			}
			if llm.ReasoningEffort() != "medium" {
				t.Fatal("global changed")
			}
		})
	}
}
