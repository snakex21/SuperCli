package agent

// Model-per-task tests: a delegated worker may run on a different
// provider (config `task_model` → AgentTool.WorkerProvider) while the
// default stays byte-identical to the single-model behaviour, and an
// unreachable worker backend falls back to the coordinator's provider
// after a single lazy probe.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
)

// captureProviderFactory builds a real child Loop with the provider the
// AgentTool selected (unlike childLoopFactory it does NOT substitute a
// stub), recording that provider for inspection.
func captureProviderFactory(got *[]llm.Provider) LoopFactory {
	return func(cfg LoopConfig) (*Loop, error) {
		*got = append(*got, cfg.Provider)
		return NewLoop(cfg)
	}
}

func newModelTestTool(t *testing.T, got *[]llm.Provider) *AgentTool {
	t.Helper()
	reg := NewSubAgentRegistry()
	MustRegisterAll(reg, BuiltinSubAgents())
	at, err := NewAgentTool(reg, nil, newTestBaseRegistry(),
		&stubReplyProvider{name: "main-model", reply: "main done"}, nil,
		captureProviderFactory(got))
	if err != nil {
		t.Fatalf("NewAgentTool: %v", err)
	}
	return at
}

// TestTask_DefaultWorkerInheritsProvider: without a WorkerProvider the
// child loop runs on the coordinator's provider and the summary keeps
// its historical single-model format (no model= suffix).
func TestTask_DefaultWorkerInheritsProvider(t *testing.T) {
	var got []llm.Provider
	at := newModelTestTool(t, &got)

	res, err := at.execute(context.Background(), json.RawMessage(`{"prompt":"do it"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("execute: err=%v resErr=%v", err, res.Err)
	}
	if len(got) != 1 || got[0] != at.Provider {
		t.Fatalf("worker should inherit the coordinator provider, got %v", got)
	}
	if strings.Contains(res.Text, "model=") {
		t.Errorf("default summary must not carry a model suffix: %q", res.Text)
	}
}

// TestTask_WorkerProviderOverride: a configured WorkerProvider (no
// ping) routes the child loop to the other backend and the summary
// names it.
func TestTask_WorkerProviderOverride(t *testing.T) {
	var got []llm.Provider
	at := newModelTestTool(t, &got)
	worker := &stubReplyProvider{name: "worker-model", reply: "worker done"}
	at.WorkerProvider = worker

	res, err := at.execute(context.Background(), json.RawMessage(`{"prompt":"do it"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("execute: err=%v resErr=%v", err, res.Err)
	}
	if len(got) != 1 || got[0] != llm.Provider(worker) {
		t.Fatalf("worker should run on WorkerProvider, got %v", got)
	}
	if !strings.Contains(res.Text, "model=worker-model") {
		t.Errorf("summary should name the worker model: %q", res.Text)
	}
	if !strings.Contains(res.Text, "worker done") {
		t.Errorf("worker result missing: %q", res.Text)
	}
}

// TestTask_WorkerPingFailureFallsBack: a failing WorkerPing downgrades
// every delegation to the coordinator's provider; the probe runs only
// once even across repeated delegations, and the fallback is not a
// hard error.
func TestTask_WorkerPingFailureFallsBack(t *testing.T) {
	var got []llm.Provider
	at := newModelTestTool(t, &got)
	at.WorkerProvider = &stubReplyProvider{name: "worker-model", reply: "never"}
	var pings atomic.Int32
	at.WorkerPing = func(context.Context) error {
		pings.Add(1)
		return errors.New("connection refused")
	}

	for i := 0; i < 2; i++ {
		res, err := at.execute(context.Background(), json.RawMessage(`{"prompt":"do it"}`))
		if err != nil || res.Err != nil {
			t.Fatalf("execute %d: err=%v resErr=%v", i, err, res.Err)
		}
		if strings.Contains(res.Text, "model=") {
			t.Errorf("fallback summary must not carry a model suffix: %q", res.Text)
		}
	}
	if len(got) != 2 || got[0] != at.Provider || got[1] != at.Provider {
		t.Fatalf("both workers should fall back to the coordinator provider, got %v", got)
	}
	if n := pings.Load(); n != 1 {
		t.Errorf("ping should run exactly once, ran %d times", n)
	}
}

// TestTask_WorkerPingSuccessUsesWorker: a passing probe keeps the
// worker on the configured backend.
func TestTask_WorkerPingSuccessUsesWorker(t *testing.T) {
	var got []llm.Provider
	at := newModelTestTool(t, &got)
	worker := &stubReplyProvider{name: "worker-model", reply: "ok"}
	at.WorkerProvider = worker
	at.WorkerPing = func(context.Context) error { return nil }

	res, err := at.execute(context.Background(), json.RawMessage(`{"prompt":"do it"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("execute: err=%v resErr=%v", err, res.Err)
	}
	if len(got) != 1 || got[0] != llm.Provider(worker) {
		t.Fatalf("worker should run on WorkerProvider after a passing ping, got %v", got)
	}
	if !strings.Contains(res.Text, "model=worker-model") {
		t.Errorf("summary should name the worker model: %q", res.Text)
	}
}
