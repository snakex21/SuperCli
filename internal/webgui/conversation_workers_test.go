package webgui

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

type asyncTaskProvider struct{ calls atomic.Int32 }

func (*asyncTaskProvider) Name() string { return "async-task-fixture" }

func (p *asyncTaskProvider) Complete(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
	if p.calls.Add(1) == 1 {
		return workerSteeringWireCall("bg-1", "task", map[string]any{"prompt": "build the mod", "async": true})
	}
	return workerSteeringWireDeltas(llm.Delta{Content: "Started.", FinishReason: "stop"}), nil
}

type blockingWorkerProvider struct{ release chan struct{} }

func (*blockingWorkerProvider) Name() string { return "blocking-worker" }

func (p *blockingWorkerProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	select {
	case <-p.release:
	case <-ctx.Done():
	}
	return workerSteeringWireDeltas(llm.Delta{Content: "done", FinishReason: "stop"}), nil
}

// Workers started by an earlier run live in that run's (detached) loop. A new
// run of the conversation counts them for its command cap, and the panel's
// background counter shows the same number.
func TestNewRunCountsWorkersStartedByEarlierRuns(t *testing.T) {
	srv := newTestServer(t, false)
	eng := srv.eng
	reg := tools.NewRegistry()
	earlier, err := agent.NewLoop(agent.LoopConfig{Provider: &asyncTaskProvider{}, Registry: reg, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	worker := &blockingWorkerProvider{release: make(chan struct{})}
	defer close(worker.release)
	specs := agent.NewSubAgentRegistry()
	agent.MustRegisterAll(specs, agent.BuiltinSubAgents())
	task, err := agent.NewAgentTool(specs, earlier, reg, worker, nil, agent.NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	reg.MustRegister(task.Spec())
	reg.MarkAlwaysOn("task")
	bg := eng.backgroundFor("conv-1", t.TempDir())
	bg.attach(earlier, false)
	events, err := earlier.Run(context.Background(), "zbuduj w tle")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	bg.attach(earlier, true) // the earlier run ended: its loop is detached
	if earlier.ActiveBackgroundWorkers() != 1 {
		t.Fatalf("fixture: earlier run workers = %d", earlier.ActiveBackgroundWorkers())
	}
	next, err := agent.NewLoop(agent.LoopConfig{Provider: &asyncTaskProvider{}, Registry: tools.NewRegistry(), MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	if next.ConversationActiveWorkers() != 0 {
		t.Fatal("fixture: a loop alone knows no other workers")
	}
	bg.attach(next, false)
	if got := next.ConversationActiveWorkers(); got != 1 {
		t.Fatalf("new run sees %d conversation workers, want 1 (its command cap would not apply)", got)
	}
	if got := bg.running(); got != 1 {
		t.Fatalf("panel background counter = %d, want 1", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && eng.BackgroundAgentCounts()["conv-1"] != 1 {
		time.Sleep(20 * time.Millisecond)
	}
	if eng.BackgroundAgentCounts()["conv-1"] != 1 {
		t.Fatalf("session list counter = %v", eng.BackgroundAgentCounts())
	}
}
