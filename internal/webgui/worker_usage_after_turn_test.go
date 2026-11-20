package webgui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
)

// usageAfterTurnProvider: the coordinator delegates one async worker and
// ends its turn; the worker answers (with provider-reported usage) only
// after the turn has ended.
type usageAfterTurnProvider struct {
	parentCalls atomic.Int32
	release     chan struct{}
}

func (*usageAfterTurnProvider) Name() string { return "usage-after-turn" }

func (p *usageAfterTurnProvider) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	parent := false
	for _, def := range defs {
		parent = parent || def.Name == "task"
	}
	usage := func(in, out int) llm.Delta {
		return llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: in, Output: out, Total: in + out}}
	}
	if !parent {
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return workerSteeringWireDeltas(llm.Delta{Content: "WORKER_USAGE_REPORT"}, usage(4321, 123)), nil
	}
	switch p.parentCalls.Add(1) {
	case 1:
		return workerSteeringWireDeltas(llm.Delta{ToolCall: &llm.ToolCall{ID: "spawn-usage", Name: "task", Arguments: `{"agent":"explore","prompt":"usage probe","async":true}`}},
			llm.Delta{FinishReason: "tool_calls", Usage: &llm.Usage{Input: 100, Output: 10, Total: 110}}), nil
	default:
		return workerSteeringWireDeltas(llm.Delta{Content: "ok"}, usage(100, 5)), nil
	}
}

// 1.0.37: model calls of a background worker that run after the
// coordinator's turn ended are recorded in the session's usage rows
// (source "worker"); before, they were not recorded at all.
func TestBackgroundWorkerUsageRecordedAfterTurnEnds(t *testing.T) {
	srv := newTestServer(t, false)
	srv.eng.setHome(t.TempDir())
	if err := os.WriteFile(filepath.Join(srv.eng.DataDir(), "config.toml"), []byte("orchestrator = true\npreflight_repo = false\nbackground_wake_debounce_ms = 30\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &usageAfterTurnProvider{release: make(chan struct{})}
	srv.eng.mu.Lock()
	srv.eng.prov = llm.Metered(provider, "fixture", "", func(llm.CallStat) {})
	srv.eng.mu.Unlock()
	sid := ""
	done := make(chan error, 1)
	go func() {
		done <- srv.eng.runStream(context.Background(), "Delegate a background probe.", "", "", func(ev wireEvent) {
			if ev.Type == "session" {
				sid = ev.SessionID
			}
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		close(provider.release)
		t.Fatalf("turn blocked on its background worker (parent calls %d)", provider.parentCalls.Load())
	}
	if sid == "" {
		t.Fatal("no session")
	}
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	countWorker := func() (rows int, input int64) {
		recs, err := store.ReadUsage(context.Background(), sid)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range recs {
			if r.Source == "worker" {
				rows++
				input += r.Input
			}
		}
		return
	}
	if n, _ := countWorker(); n != 0 {
		t.Fatalf("worker rows before the worker answered = %d", n)
	}
	close(provider.release) // the turn is over; the worker answers now
	deadline := time.Now().Add(10 * time.Second)
	for {
		n, in := countWorker()
		if n == 1 && in == 4321 {
			break
		}
		if time.Now().After(deadline) {
			recs, _ := store.ReadUsage(context.Background(), sid)
			var srcs []string
			for _, r := range recs {
				srcs = append(srcs, r.Source)
			}
			t.Fatalf("worker usage after the turn not recorded: rows=%d input=%d sources=%s", n, in, strings.Join(srcs, ","))
		}
		time.Sleep(20 * time.Millisecond)
	}
}
