package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// 1.0.37: a running worker reports what it is doing (model / tool), when it
// last made progress, live tool progress, and its tool journal so far. Fake
// provider and tool only.

type livenessProvider struct {
	gate  chan struct{}
	calls int
	mu    sync.Mutex
}

func (*livenessProvider) Name() string { return "liveness-fixture" }

func (p *livenessProvider) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	select {
	case <-p.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.mu.Unlock()
	out := make(chan llm.Delta, 2)
	if n == 1 {
		out <- llm.Delta{ToolCall: &llm.ToolCall{ID: "slow-1", Name: "slow_build", Arguments: `{"target":"all"}`}}
		out <- llm.Delta{FinishReason: "tool_calls"}
	} else {
		out <- llm.Delta{Content: "LIVENESS_REPORT", FinishReason: "stop"}
	}
	close(out)
	return out, nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWorkerLivenessPhasesProgressAndLiveJournal(t *testing.T) {
	prov := &livenessProvider{gate: make(chan struct{}, 4)}
	release := make(chan struct{})
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "slow_build", Description: "slow", Schema: `{"type":"object","properties":{"target":{"type":"string"}}}`,
		Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
			tools.ReportToolProgress(ctx, tools.ToolProgress{Message: "[3/9] compiling engine"})
			select {
			case <-release:
			case <-ctx.Done():
				return tools.Result{}, ctx.Err()
			}
			return tools.Result{Text: "build ok"}, nil
		}})
	reg.MarkAlwaysOn("slow_build")
	loop, err := NewLoop(LoopConfig{Provider: prov, Registry: reg, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	workers := NewWorkerRegistry()
	w := workers.Add("code", "LIVENESS_TASK", loop)
	var mu sync.Mutex
	var progress []WorkerProgressEvent
	w.progress = func(ev WorkerProgressEvent) {
		mu.Lock()
		progress = append(progress, ev)
		mu.Unlock()
	}
	if _, ok := w.LiveActivity(); ok {
		t.Fatal("idle worker reported a live journal")
	}
	done := make(chan error, 1)
	go func() {
		_, err := runWorkerInvocation(context.Background(), w, "LIVENESS_TASK", nil)
		done <- err
	}()

	// Waiting for the model: phase "model", progress time set.
	waitFor(t, "model phase", func() bool { return w.Snapshot().Live.Phase == "model" })
	s := w.Snapshot()
	if s.Status != "running" || s.Live.LastProgressAt.IsZero() || s.Live.PhaseSince.IsZero() {
		t.Fatalf("model phase snapshot = %+v", s)
	}
	started := s.Live.LastProgressAt

	// The tool runs: phase "tool" with its name and live progress line.
	prov.gate <- struct{}{}
	waitFor(t, "tool phase with progress", func() bool {
		l := w.Snapshot().Live
		return l.Phase == "tool" && l.ToolProgress != ""
	})
	l := w.Snapshot().Live
	if l.Tool != "slow_build" || l.OpenCalls != 1 || !strings.Contains(l.ToolProgress, "compiling engine") || l.LastProgressAt.Before(started) {
		t.Fatalf("tool phase = %+v", l)
	}
	data, ok := w.LiveActivity()
	if !ok {
		t.Fatal("running worker has no live journal")
	}
	var journal struct {
		ID    string `json:"id"`
		Run   int    `json:"run"`
		Calls []struct {
			ID        string `json:"id"`
			Tool      string `json:"tool"`
			Completed bool   `json:"completed"`
		} `json:"calls"`
	}
	if err := json.Unmarshal(data, &journal); err != nil {
		t.Fatal(err)
	}
	if journal.ID != w.ID || journal.Run != 1 || len(journal.Calls) != 1 || journal.Calls[0].Tool != "slow_build" || journal.Calls[0].Completed {
		t.Fatalf("live journal = %s", data)
	}
	mu.Lock()
	var sawProgress bool
	for _, ev := range progress {
		sawProgress = sawProgress || (ev.Kind == "tool_progress" && ev.CallID == "slow-1" && ev.Tool == "slow_build" && strings.Contains(ev.Output, "compiling"))
	}
	mu.Unlock()
	if !sawProgress {
		t.Fatalf("tool progress not forwarded to the GUI: %+v", progress)
	}

	// Result back: model phase again, then the run ends and liveness clears.
	close(release)
	waitFor(t, "model phase after the tool", func() bool { l := w.Snapshot().Live; return l.Phase == "model" && l.OpenCalls == 0 })
	if data, ok := w.LiveActivity(); !ok || !strings.Contains(string(data), `"completed":true`) {
		t.Fatalf("journal after result = %s ok=%v", data, ok)
	}
	prov.gate <- struct{}{}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s := w.Snapshot(); s.Status != "done" || s.Live.Phase != "" {
		t.Fatalf("finished worker = status %q live %+v", s.Status, s.Live)
	}
	if _, ok := w.LiveActivity(); ok {
		t.Fatal("finished worker still reports a live journal")
	}
}
