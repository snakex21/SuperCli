package darwin

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"supercli/internal/llm"
)

// darwin_models assigns candidates round-robin; nil slots fall back to the
// active model, and the rendered result names each candidate's model.
func TestDarwinModelsRoundRobin(t *testing.T) {
	d := newTestTool(t)
	d.SetProviderResolver(func() llm.Provider { return selectedProvider{name: "active"} })
	d.SetModels([]llm.Provider{selectedProvider{name: "local"}, nil, selectedProvider{name: "cloud"}})
	var mu sync.Mutex
	var seen []string
	d.SetLoopFactory(func(cfg LoopConfig) (Loop, error) {
		mu.Lock()
		seen = append(seen, cfg.Provider.Name())
		mu.Unlock()
		return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "answer"}}}, nil
	})
	d.SetSequential(true)
	res, err := d.Spec().Fn(context.Background(), json.RawMessage(`{"prompt":"x","pool_size":4,"no_worktree":true,"judge":"heuristic"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("%v %+v", err, res)
	}
	if got := strings.Join(seen, ","); got != "local,active,cloud,local" {
		t.Fatalf("models = %s", got)
	}
	if !strings.Contains(res.Text, "agent-3, cloud") {
		t.Fatalf("result does not name candidate models:\n%s", res.Text)
	}
}

// auto_merge from the model is ignored unless darwin_auto_merge allows it.
func TestDarwinAutoMergeNeedsUserOptIn(t *testing.T) {
	d := newTestTool(t)
	d.SetLoopFactory(func(cfg LoopConfig) (Loop, error) {
		return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "answer"}}}, nil
	})
	raw := json.RawMessage(`{"prompt":"x","pool_size":1,"no_worktree":true,"judge":"heuristic","auto_merge":true}`)
	res, err := d.Spec().Fn(context.Background(), raw)
	if err != nil || res.Err != nil {
		t.Fatalf("%v %+v", err, res)
	}
	if !strings.Contains(res.Text, "auto_merge ignored") {
		t.Fatalf("missing ignore note:\n%s", res.Text)
	}
	d.SetAllowAutoMerge(true)
	res, err = d.Spec().Fn(context.Background(), raw)
	if err != nil || res.Err != nil {
		t.Fatalf("%v %+v", err, res)
	}
	if strings.Contains(res.Text, "auto_merge ignored") {
		t.Fatalf("opt-in still ignored:\n%s", res.Text)
	}
}
