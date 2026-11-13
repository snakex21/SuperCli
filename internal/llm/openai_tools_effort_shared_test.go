package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Latency regression: the tools+effort rejection was remembered only on one
// provider instance. Every new instance for the same endpoint and model (each
// batch run, each task worker, a model re-selection in the GUI) paid another
// doomed 400 before its first token: 0.4-1.7 s measured live against
// api.openai.com with gpt-5.4-nano. One rejection per process is enough.
func TestOpenAIToolsEffortRejectionIsSharedAcrossProviderInstances(t *testing.T) {
	t.Cleanup(func() { _ = SetReasoningEffort(""); clearReasoningEffortSupport(); resetToolsEffortRejected() })
	_ = SetReasoningEffort("high")
	var mu sync.Mutex
	var rejected, served int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		mu.Lock()
		defer mu.Unlock()
		if _, ok := m["reasoning_effort"]; ok && m["tools"] != nil {
			rejected++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(chatToolsEffortRejection))
			return
		}
		served++
		sseResponse(w, `{"choices":[{"index":0,"delta":{"content":"ok"}}]}`, `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	tools := []ToolDef{{Name: "read", Description: "r", Schema: `{"type":"object","properties":{}}`}}
	run := func(model string) {
		p, err := NewOpenAI(OpenAIConfig{BaseURL: srv.URL, Model: model})
		if err != nil {
			t.Fatal(err)
		}
		ch, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, tools)
		if err != nil {
			t.Fatal(err)
		}
		for d := range ch {
			if d.Err != nil {
				t.Fatalf("%s: %v", model, d.Err)
			}
		}
	}
	run("gpt-5.4")
	run("gpt-5.4") // fresh instance, same endpoint+model: no second 400
	if rejected != 1 || served != 2 {
		t.Fatalf("rejected=%d served=%d, want 1 doomed request in total", rejected, served)
	}
	// Another model on the same endpoint still tries the user's level first.
	run("gpt-5.4-mini")
	if rejected != 2 || served != 3 {
		t.Fatalf("other model: rejected=%d served=%d, want its own first attempt", rejected, served)
	}
}
