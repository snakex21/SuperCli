package agent

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// fakeWorkerBackend simulates either a cloud API (parallel requests) or a
// single local server slot (LM Studio: requests serialized server-side). Each
// request streams `chunks` deltas with a small delay, like a long reasoning
// stream. It is safe for concurrent workers.
type fakeWorkerBackend struct {
	serialized bool
	slot       sync.Mutex
	inflight   atomic.Int32
	maxSeen    atomic.Int32
	completed  atomic.Int32
	chunks     int
	delay      time.Duration
}

func (p *fakeWorkerBackend) Name() string         { return "fake-worker" }
func (p *fakeWorkerBackend) SupportsVision() bool { return false }

func (p *fakeWorkerBackend) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta, 1)
	go func() {
		defer close(ch)
		if p.serialized {
			p.slot.Lock()
			defer p.slot.Unlock()
		}
		n := p.inflight.Add(1)
		defer p.inflight.Add(-1)
		for {
			seen := p.maxSeen.Load()
			if n <= seen || p.maxSeen.CompareAndSwap(seen, n) {
				break
			}
		}
		for i := 0; i < p.chunks; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(p.delay):
			}
			ch <- llm.Delta{Content: "part "}
		}
		ch <- llm.Delta{Content: "WORKER_DONE", FinishReason: "stop", Usage: &llm.Usage{Total: 1}}
		p.completed.Add(1)
	}()
	return ch, nil
}

// gatedCoordinator answers the user turn only after `gate` is closed (or the
// turn is cancelled), so a test can act while that user turn is in flight.
type gatedCoordinator struct {
	*stubProvider
	gate  chan struct{}
	user  chan struct{}
	mu    sync.Mutex
	gated bool
}

func (p *gatedCoordinator) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	p.mu.Lock()
	gated := false
	for _, m := range msgs {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, "SECOND_QUESTION") {
			gated = true
		}
	}
	gated = gated && !p.gated
	if gated {
		p.gated = true
	}
	p.mu.Unlock()
	if gated {
		close(p.user)
		select {
		case <-p.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return p.stubProvider.Complete(ctx, msgs, defs)
}

func startTwoBackgroundWorkers(t *testing.T, backend *fakeWorkerBackend) (*Loop, *gatedCoordinator, chan Event) {
	t.Helper()
	reg := tools.NewRegistry()
	base := makeScriptedProvider("unused")
	base.scripts = [][]llm.Delta{
		{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "task", Arguments: `{"prompt":"worker A","async":true}`}},
			{ToolCall: &llm.ToolCall{ID: "b", Name: "task", Arguments: `{"prompt":"worker B","async":true}`}, FinishReason: "tool_calls"},
		},
		{{Content: "Both started.", FinishReason: "stop"}},
		{{Content: "Answer.", FinishReason: "stop"}},
	}
	coordinator := &gatedCoordinator{stubProvider: base, gate: make(chan struct{}), user: make(chan struct{})}
	parent, err := NewLoop(LoopConfig{Provider: coordinator, Registry: reg, MaxSteps: 8})
	if err != nil {
		t.Fatal(err)
	}
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	task, err := NewAgentTool(specs, parent, reg, backend, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	reg.MustRegister(task.Spec())
	reg.MarkAlwaysOn("task")
	ext := make(chan Event, 256)
	parent.SetExternalSink(ext)
	parent.SetBackgroundWake(WakeConfig{Debounce: testWakeDebounce})
	if !drainRun(t, mustRun(t, parent, "Start two background workers.")) {
		t.Fatal("first turn failed")
	}
	if parent.ActiveBackgroundWorkers() != 2 {
		t.Fatalf("active=%d", parent.ActiveBackgroundWorkers())
	}
	return parent, coordinator, ext
}

func waitWorkersDone(t *testing.T, parent *Loop, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for parent.ActiveBackgroundWorkers() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("background workers stalled: active=%d", parent.ActiveBackgroundWorkers())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A new user message while workers run must neither wait for them nor pause
// them; cloud workers stream truly in parallel, a serialized local backend
// still finishes both. Their reports then wake the conversation.
func TestBackgroundWorkersKeepRunningDuringUserTurn(t *testing.T) {
	for _, serialized := range []bool{false, true} {
		name := "cloud-parallel"
		if serialized {
			name = "local-serialized"
		}
		t.Run(name, func(t *testing.T) {
			backend := &fakeWorkerBackend{serialized: serialized, chunks: 6, delay: 15 * time.Millisecond}
			parent, coordinator, ext := startTwoBackgroundWorkers(t, backend)
			events := mustRun(t, parent, "SECOND_QUESTION while they work")
			<-coordinator.user
			// The user's turn is in flight: the workers must progress meanwhile.
			waitWorkersDone(t, parent, 5*time.Second)
			if backend.completed.Load() != 2 {
				t.Fatalf("completed=%d", backend.completed.Load())
			}
			close(coordinator.gate)
			if !drainRun(t, events) {
				t.Fatal("user turn failed")
			}
			if serialized && backend.maxSeen.Load() != 1 {
				t.Fatalf("serialized backend saw %d concurrent requests", backend.maxSeen.Load())
			}
			if !serialized && backend.maxSeen.Load() < 2 {
				t.Fatalf("cloud workers did not run in parallel (max in flight %d)", backend.maxSeen.Load())
			}
			// Reports delivered during the user turn were seen by it or wake now.
			if parent.UnseenBackgroundReports() > 0 {
				if _, ok := waitWakeEvent(t, ext, time.Second); !ok {
					t.Fatal("unseen reports did not wake the conversation")
				}
			}
		})
	}
}

// Interrupting (Esc/Stop) the main turn cancels only that turn.
func TestInterruptingMainTurnDoesNotStopBackgroundWorkers(t *testing.T) {
	backend := &fakeWorkerBackend{chunks: 8, delay: 15 * time.Millisecond}
	parent, coordinator, ext := startTwoBackgroundWorkers(t, backend)
	ctx, cancel := context.WithCancel(context.Background())
	events, err := parent.Run(ctx, "SECOND_QUESTION then interrupted")
	if err != nil {
		t.Fatal(err)
	}
	<-coordinator.user
	cancel()
	for range events {
	}
	waitWorkersDone(t, parent, 5*time.Second)
	if backend.completed.Load() != 2 {
		t.Fatalf("interrupt killed workers: completed=%d", backend.completed.Load())
	}
	wake, ok := waitWakeEvent(t, ext, 2*time.Second)
	if !ok || wake.Unseen != 2 {
		t.Fatalf("reports after interrupt: %+v ok=%v", wake, ok)
	}
	if !strings.Contains(parent.Messages[len(parent.Messages)-1].Content, "WORKER_DONE") {
		t.Fatal("last report missing from history")
	}
}
