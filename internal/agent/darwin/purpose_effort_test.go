package darwin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestDarwinCandidateAndJudgeEffortWire(t *testing.T) {
	initial := llm.ReasoningEffort()
	t.Cleanup(func() { _ = llm.SetReasoningEffort(initial) })
	_ = llm.SetReasoningEffort("medium")
	for _, level := range []string{"", "high"} {
		t.Run("level="+level, func(t *testing.T) {
			var mu sync.Mutex
			var efforts []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Effort string `json:"reasoning_effort"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				mu.Lock()
				efforts = append(efforts, body.Effort)
				mu.Unlock()
				reply := `{"winner":0,"score":0.9,"reason":"tests pass"}`
				payload, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": reply}, "finish_reason": "stop"}}})
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
			}))
			defer srv.Close()
			p, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: srv.URL, Model: "gpt-5"})
			if err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			dt := NewDarwinTool(p, reg, t.TempDir(), "Reply with a test result; do not use tools.")
			dt.SetReasoningEffort(level)
			dt.SetSequential(true)
			dt.SetLoopFactory(AgentLoopAdapter(reg, nil))
			// Also exercise explicit candidate-model and judge overrides.
			dt.SetModels([]llm.Provider{p, p})
			dt.SetJudgeProvider(p)
			ctx, cancel := context.WithTimeout(llm.WithReasoningEffort(context.Background(), "low"), 15*time.Second)
			defer cancel()
			res, err := dt.run(ctx, json.RawMessage(`{"prompt":"test","pool_size":2,"judge":"llm","no_worktree":true}`))
			if err != nil || res.Err != nil {
				t.Fatalf("Darwin: %v %v", err, res.Err)
			}
			want := level
			if want == "" {
				want = "medium"
			}
			mu.Lock()
			defer mu.Unlock()
			if len(efforts) != 3 {
				t.Fatalf("requests=%d want 2 candidates + judge", len(efforts))
			}
			for i, got := range efforts {
				if got != want {
					t.Fatalf("request %d effort=%q want=%q", i, got, want)
				}
			}
			if llm.ReasoningEffort() != "medium" || llm.ReasoningEffort(ctx) != "low" {
				t.Fatal("effort leaked")
			}
		})
	}
}
