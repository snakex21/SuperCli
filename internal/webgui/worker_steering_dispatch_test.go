package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
)

type workerSteeringWireProvider struct {
	started          chan struct{}
	release          chan struct{}
	finished         chan struct{}
	releaseOnce      sync.Once
	finishedOnce     sync.Once
	parentCalls      atomic.Int32
	workerCalls      atomic.Int32
	correctionCopies atomic.Int32
	traceMu          sync.Mutex
	traces           []string
}

func (*workerSteeringWireProvider) Name() string { return "worker-steering-wire-fixture" }

func workerSteeringWireDeltas(deltas ...llm.Delta) <-chan llm.Delta {
	out := make(chan llm.Delta, len(deltas))
	for _, delta := range deltas {
		out <- delta
	}
	close(out)
	return out
}

func workerSteeringWireCall(id, name string, args map[string]any) (<-chan llm.Delta, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return workerSteeringWireDeltas(llm.Delta{ToolCall: &llm.ToolCall{ID: id, Name: name, Arguments: string(raw)}}, llm.Delta{FinishReason: "tool_calls"}), nil
}

func (p *workerSteeringWireProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	user := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleUser {
			user = msgs[i].Content
			break
		}
	}
	p.traceMu.Lock()
	p.traces = append(p.traces, user[:min(len(user), 160)])
	p.traceMu.Unlock()
	if strings.HasPrefix(user, "worker-steering-original") {
		if p.workerCalls.Add(1) != 1 {
			return nil, fmt.Errorf("original worker request repeated")
		}
		out := make(chan llm.Delta, 2)
		close(p.started)
		go func() {
			defer close(out)
			select {
			case <-p.release:
			case <-ctx.Done():
				return
			}
			out <- llm.Delta{Content: "initial worker response"}
			out <- llm.Delta{FinishReason: "stop"}
		}()
		return out, nil
	}
	if user == "worker-steering-correction" {
		p.workerCalls.Add(1)
		for _, msg := range msgs {
			if msg.Role == llm.RoleUser && msg.Content == user {
				p.correctionCopies.Add(1)
			}
		}
		return workerSteeringWireDeltas(llm.Delta{Content: "corrected worker result"}, llm.Delta{FinishReason: "stop"}), nil
	}
	switch p.parentCalls.Add(1) {
	case 1:
		return workerSteeringWireCall("spawn-steering-worker", "task", map[string]any{"prompt": "worker-steering-original", "async": true})
	case 2:
		select {
		case <-p.started:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return workerSteeringWireCall("steer-active-worker", "invoke_tool", map[string]any{"tool": "send_message", "args": map[string]any{"to": "worker-1", "message": "worker-steering-correction", "mode": "steer"}})
	default:
		select {
		case <-p.finished:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return workerSteeringWireDeltas(llm.Delta{Content: "coordinator finished"}, llm.Delta{FinishReason: "stop"}), nil
	}
}

func TestWorkerSteeringEnvelopeUsesSameRunAndReachesWebStream(t *testing.T) {
	root := t.TempDir()
	eng, err := NewEngine(echoConfig(), root, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	provider := &workerSteeringWireProvider{started: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	eng.prov = provider
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var queued bool
	var spawnError, spawnOutput string
	var delivered, started int
	err = eng.runStream(ctx, "implement worker steering fixture", "", "", func(ev wireEvent) {
		if ev.Type == "tool_result" && ev.ID == "spawn-steering-worker" {
			spawnError, spawnOutput = ev.Err, ev.Output
		}
		if ev.Type == "tool_result" && ev.ID == "steer-active-worker" {
			queued = ev.Err == ""
			if ev.Err != "" {
				t.Errorf("steer through stable dispatcher failed: %s", ev.Err)
			}
			provider.releaseOnce.Do(func() { close(provider.release) })
		}
		if ev.Type == "worker_progress" && ev.ID == "worker-1" {
			if ev.Kind == "started" {
				started++
			}
			if ev.Kind == "steering_delivered" {
				delivered++
				if ev.Run != 1 || ev.Err != "" || ev.CallID == "" {
					t.Errorf("incorrect steering receipt: %+v", ev)
				}
			}
			if ev.Kind == "finished" {
				provider.finishedOnce.Do(func() { close(provider.finished) })
			}
		}
	})
	provider.releaseOnce.Do(func() { close(provider.release) })
	if err != nil {
		provider.traceMu.Lock()
		traces := append([]string(nil), provider.traces...)
		provider.traceMu.Unlock()
		t.Fatalf("%v; queued=%v delivered=%d started=%d parent=%d worker=%d traces=%q spawnError=%q spawnOutput=%q", err, queued, delivered, started, provider.parentCalls.Load(), provider.workerCalls.Load(), traces, spawnError, spawnOutput)
	}
	worker, ok := eng.workers.Get("worker-1")
	if !ok {
		t.Fatal("worker missing")
	}
	snapshot := worker.Snapshot()
	if !queued || delivered != 1 || started != 1 || snapshot.Runs != 1 || snapshot.Status != "done" {
		t.Fatalf("queued=%v delivered=%d started=%d worker=%+v", queued, delivered, started, snapshot)
	}
	if provider.workerCalls.Load() != 2 || provider.correctionCopies.Load() != 1 {
		t.Fatalf("worker calls=%d correction copies=%d", provider.workerCalls.Load(), provider.correctionCopies.Load())
	}
}
