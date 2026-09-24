package darwin

import (
	"context"
	"encoding/json"
	"supercli/internal/llm"
	"testing"
)

type selectedProvider struct {
	providerStub
	name string
}

func (p selectedProvider) Name() string { return p.name }

func TestDarwinResolvesCurrentModelForEveryInvocation(t *testing.T) {
	d := newTestTool(t)
	var selected llm.Provider = selectedProvider{name: "first"}
	d.SetProviderResolver(func() llm.Provider { return selected })
	seen := make(chan string, 2)
	d.SetLoopFactory(func(cfg LoopConfig) (Loop, error) {
		seen <- cfg.Provider.Name()
		return &stubLoop{script: []LoopEvent{LoopDoneEvent{Text: "answer"}}}, nil
	})
	for _, name := range []string{"cloud", "local"} {
		selected = selectedProvider{name: name}
		d.SetSequential(name == "local")
		result, err := d.Spec().Fn(context.Background(), json.RawMessage("{\"prompt\":\"compare\",\"pool_size\":1,\"no_worktree\":true,\"judge\":\"heuristic\"}"))
		if err != nil || result.Err != nil {
			t.Fatalf("%v %+v", err, result)
		}
		if got := <-seen; got != name {
			t.Fatalf("used stale model %s instead of %s", got, name)
		}
	}
}
