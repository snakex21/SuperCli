package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestAgentToolInjectedProviderSurvivesParentSwitchAndFailedWorker(t *testing.T) {
	for _, failedWorker := range []bool{false, true} {
		name := "default"
		if failedWorker {
			name = "failed-worker"
		}
		t.Run(name, func(t *testing.T) {
			// Equal names must not turn distinct provider instances into inherited
			// defaults; they can represent different servers or scripted backends.
			parentProvider := &stubReplyProvider{name: "same-model", reply: "wrong parent result"}
			injected := &stubReplyProvider{name: "same-model", reply: "injected child result"}
			base := newTestBaseRegistry()
			parent := makeLoop(t, parentProvider, base, "")
			reg := NewSubAgentRegistry()
			MustRegisterAll(reg, BuiltinSubAgents())
			var providers []llm.Provider
			at, err := NewAgentTool(reg, parent, base, injected, nil, captureProviderFactory(&providers))
			if err != nil {
				t.Fatal(err)
			}
			probes := 0
			if failedWorker {
				at.WorkerProvider = &stubReplyProvider{name: "offline-worker", reply: "wrong worker result"}
				at.WorkerPing = func(context.Context) error {
					probes++
					return errors.New("offline")
				}
			}
			for i := 0; i < 2; i++ {
				if i == 1 {
					parent.SetModel(&stubReplyProvider{name: "switched-parent", reply: "wrong switched result"})
				}
				result, err := at.execute(context.Background(), json.RawMessage(`{"prompt":"Report ready."}`))
				if err != nil || result.Err != nil {
					t.Fatalf("worker %d: %v %v", i, err, result.Err)
				}
				if len(providers) != i+1 || providers[i] != injected || !strings.Contains(result.Text, "injected child result") {
					t.Fatalf("worker %d ignored injected backend: providers=%v result=%q", i, providers, result.Text)
				}
			}
			wantProbes := 0
			if failedWorker {
				wantProbes = 1
			}
			if probes != wantProbes || at.Provider != injected {
				t.Fatalf("probes=%d want=%d explicit-provider-mutated=%v", probes, wantProbes, at.Provider != injected)
			}
		})
	}
}

// A valid custom provider can be a value containing slices. Construction must
// retain the explicit backend without comparing incomparable interface values.
type incomparableAgentToolProvider struct {
	*stubReplyProvider
	values []string
}

func TestAgentToolIncomparableProviderConstruction(t *testing.T) {
	provider := incomparableAgentToolProvider{
		stubReplyProvider: &stubReplyProvider{name: "custom", reply: "ready"},
		values:            []string{"not-comparable"},
	}
	base := newTestBaseRegistry()
	parent := makeLoop(t, provider, base, "")
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents())
	at, err := NewAgentTool(reg, parent, base, provider, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	parent.SetModel(&stubReplyProvider{name: "switched", reply: "wrong"})
	selected, ok := at.workerProvider(context.Background()).(incomparableAgentToolProvider)
	if !ok || selected.stubReplyProvider != provider.stubReplyProvider || len(selected.values) != 1 || selected.values[0] != "not-comparable" {
		t.Fatalf("custom injected provider was replaced: %T", at.workerProvider(context.Background()))
	}
}
