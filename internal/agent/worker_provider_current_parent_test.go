package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"supercli/internal/llm"
)

// A provider wrapper may advertise a transport/pool name separately from the
// actual model. New workers must inherit both from the selected provider.
type currentParentModelProvider struct {
	*stubReplyProvider
	model string
}

func (p *currentParentModelProvider) ModelName() string { return p.model }

func TestWorkerProviderLegacyLiteralRetainsExplicitBackend(t *testing.T) {
	startup := &stubReplyProvider{name: "startup", reply: "startup"}
	current := &stubReplyProvider{name: "current", reply: "current"}
	pinned := &stubReplyProvider{name: "pinned", reply: "pinned"}
	for _, tc := range []struct {
		name   string
		parent *Loop
		worker llm.Provider
		down   bool
		want   llm.Provider
	}{
		{"legacy-no-parent", nil, nil, false, startup},
		{"empty-parent", &Loop{}, nil, false, startup},
		{"live-parent", &Loop{provider: current}, nil, false, startup},
		{"configured-worker", &Loop{provider: current}, pinned, false, pinned},
		{"failed-worker-live-parent", &Loop{provider: current}, pinned, true, startup},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := &AgentTool{Provider: startup, ParentLoop: tc.parent, WorkerProvider: tc.worker}
			if tc.down {
				at.WorkerPing = func(context.Context) error { t.Fatal("cached failed probe must not run again"); return nil }
				at.workerProbed, at.workerDown = true, true
			}
			if got := at.workerProvider(context.Background()); got != tc.want {
				t.Fatalf("selected provider=%v, want %v", got, tc.want)
			}
			if at.Provider != startup {
				t.Fatal("provider selection mutated legacy startup configuration")
			}
		})
	}
}

func newCurrentParentModelTestTool(t *testing.T, providers *[]llm.Provider) (*AgentTool, *Loop) {
	t.Helper()
	startup := &stubReplyProvider{name: "main-model", reply: "main done"}
	base := newTestBaseRegistry()
	parent := makeLoop(t, startup, base, "")
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents())
	at, err := NewAgentTool(reg, parent, base, startup, nil, captureProviderFactory(providers))
	if err != nil {
		t.Fatalf("NewAgentTool: %v", err)
	}
	return at, parent
}

func TestTaskFallbackTracksParentSwitchAndKeepsContinuationIdentity(t *testing.T) {
	for _, failedWorker := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed-worker=%v", failedWorker), func(t *testing.T) {
			var providers []llm.Provider
			at, parent := newCurrentParentModelTestTool(t, &providers)
			startup := at.Provider
			first := &currentParentModelProvider{stubReplyProvider: &stubReplyProvider{name: "account-pool-first", reply: "first done"}, model: "actual-first-model"}
			second := &currentParentModelProvider{stubReplyProvider: &stubReplyProvider{name: "account-pool-second", reply: "second done"}, model: "actual-second-model"}
			limits := map[string]int{"first-connection/actual-first-model": 6144, "second-connection/actual-second-model": 12288}
			parent.scopedWindowFor = func(provider, model string) ContextWindowResolution {
				return ContextWindowResolution{Tokens: limits[provider+"/"+model], Source: "provider-loaded"}
			}
			probes := 0
			if failedWorker {
				at.WorkerProvider = &stubReplyProvider{name: "unavailable-worker", reply: "must not run"}
				at.WorkerContextProvider = "unavailable-connection"
				at.WorkerPing = func(context.Context) error { probes++; return errors.New("offline") }
			}
			parent.SetModel(first)
			parent.SetContextProvider("first-connection")
			result, err := at.execute(context.Background(), json.RawMessage(`{"prompt":"Report ready."}`))
			if err != nil || result.Err != nil {
				t.Fatalf("first worker: %v %v", err, result.Err)
			}
			firstWorker, ok := at.Workers.Get("worker-1")
			if !ok {
				t.Fatal("missing first worker")
			}
			firstLoop, firstIdentity := firstWorker.Loop, firstWorker.Snapshot()
			if len(providers) != 1 || providers[0] != first || firstLoop.provider != first || firstLoop.CurrentModel() != first.ModelName() || firstLoop.contextProvider != "first-connection" {
				t.Fatalf("first worker provider/model/scope mismatch: providers=%v model=%q scope=%q", providers, firstLoop.CurrentModel(), firstLoop.contextProvider)
			}
			if got := firstLoop.windowResolution(); got.Tokens != 6144 || got.Source != "provider-loaded" {
				t.Fatalf("first worker runtime context=%+v", got)
			}
			parent.SetModel(second)
			parent.SetContextProvider("second-connection")
			result, err = at.execute(context.Background(), json.RawMessage(`{"prompt":"Report the next task ready."}`))
			if err != nil || result.Err != nil {
				t.Fatalf("second worker: %v %v", err, result.Err)
			}
			secondWorker, ok := at.Workers.Get("worker-2")
			if !ok || len(providers) != 2 || providers[1] != second || secondWorker.Loop.provider != second || secondWorker.Loop.CurrentModel() != second.ModelName() || secondWorker.Loop.contextProvider != "second-connection" {
				t.Fatalf("second worker did not track current parent: providers=%v worker=%v", providers, secondWorker)
			}
			if got := secondWorker.Loop.windowResolution(); got.Tokens != 12288 || got.Source != "provider-loaded" {
				t.Fatalf("second worker runtime context=%+v", got)
			}
			result, err = NewSendMessageTool(at.Workers).execute(context.Background(), json.RawMessage(`{"to":"worker-1","message":"Continue the original task."}`))
			if err != nil || result.Err != nil {
				t.Fatalf("continuation: %v %v", err, result.Err)
			}
			continued := firstWorker.Snapshot()
			if len(providers) != 2 || firstWorker.Loop != firstLoop || firstLoop.provider != first || firstLoop.CurrentModel() != first.ModelName() || firstLoop.contextProvider != "first-connection" || continued.ID != firstIdentity.ID || continued.Model != firstIdentity.Model || continued.Runs != firstIdentity.Runs+1 {
				t.Fatal("parent switch changed an existing worker's loop/provider/model/scope/identity")
			}
			if got := firstLoop.windowResolution(); got.Tokens != 6144 {
				t.Fatalf("continued worker borrowed another connection's limit: %+v", got)
			}
			wantProbes := 0
			if failedWorker {
				wantProbes = 1
			}
			if probes != wantProbes || at.Provider != startup {
				t.Fatalf("probes=%d startup-provider-mutated=%v", probes, at.Provider != startup)
			}
		})
	}
}

func TestTaskPinnedWorkerSurvivesParentSwitch(t *testing.T) {
	var providers []llm.Provider
	at, parent := newCurrentParentModelTestTool(t, &providers)
	pinned := &currentParentModelProvider{stubReplyProvider: &stubReplyProvider{name: "worker-pool", reply: "worker done"}, model: "pinned-model"}
	at.WorkerProvider, at.WorkerContextProvider = pinned, "pinned-connection"
	parent.scopedWindowFor = func(provider, model string) ContextWindowResolution {
		if provider == "pinned-connection" && model == "pinned-model" {
			return ContextWindowResolution{Tokens: 8192, Source: "provider-loaded"}
		}
		return ContextWindowResolution{}
	}
	probes := 0
	at.WorkerPing = func(context.Context) error { probes++; return nil }
	for i := 0; i < 2; i++ {
		parent.SetModel(&stubReplyProvider{name: fmt.Sprintf("parent-model-%d", i), reply: "must not run"})
		parent.SetContextProvider(fmt.Sprintf("parent-connection-%d", i))
		result, err := at.execute(context.Background(), json.RawMessage(`{"prompt":"Report ready."}`))
		if err != nil || result.Err != nil {
			t.Fatalf("worker %d: %v %v", i, err, result.Err)
		}
		worker, ok := at.Workers.Get(fmt.Sprintf("worker-%d", i+1))
		if !ok || worker.Loop.provider != pinned || worker.Loop.CurrentModel() != pinned.ModelName() || worker.Loop.contextProvider != "pinned-connection" || worker.Loop.windowResolution().Tokens != 8192 {
			t.Fatalf("parent switch changed explicit worker backend/model/scope: %+v", worker)
		}
	}
	if len(providers) != 2 || providers[0] != pinned || providers[1] != pinned || probes != 1 {
		t.Fatalf("pinned providers=%v probes=%d", providers, probes)
	}
}

func TestWorkerProbeFailureSelectsParentAfterProbe(t *testing.T) {
	var providers []llm.Provider
	at, parent := newCurrentParentModelTestTool(t, &providers)
	current := &stubReplyProvider{name: "current", reply: "current"}
	at.WorkerProvider = &stubReplyProvider{name: "pinned", reply: "pinned"}
	at.WorkerPing = func(context.Context) error {
		parent.SetModel(current)
		return errors.New("offline")
	}
	if got := at.workerProvider(context.Background()); got != current {
		t.Fatalf("failed probe used a startup snapshot: provider=%v, want current parent", got)
	}
}
