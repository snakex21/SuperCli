package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// twoTaskCallProvider emits two `task` tool calls in one turn, then a
// final stop. It lets a test observe whether the loop runs the two task
// workers concurrently (cloud default) or one at a time (local default).
type twoTaskCallProvider struct {
	name  string
	calls int32
}

func (p *twoTaskCallProvider) Name() string         { return p.name }
func (p *twoTaskCallProvider) SupportsVision() bool { return false }
func (p *twoTaskCallProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	idx := int(atomic.AddInt32(&p.calls, 1)) - 1
	ch := make(chan llm.Delta, 4)
	go func() {
		defer close(ch)
		if idx == 0 {
			ch <- llm.Delta{Role: llm.RoleAssistant, ToolCall: &llm.ToolCall{ID: "call_a", Name: "task", Arguments: `{"prompt":"a"}`}}
			ch <- llm.Delta{Role: llm.RoleAssistant, ToolCall: &llm.ToolCall{ID: "call_b", Name: "task", Arguments: `{"prompt":"b"}`}}
			ch <- llm.Delta{FinishReason: "tool_calls"}
			return
		}
		ch <- llm.Delta{Role: llm.RoleAssistant, Content: "done"}
		ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Total: 1}}
	}()
	return ch, nil
}

// concurrencyTaskRegistry registers a `task` tool that records the peak
// number of concurrent executions, so a test can distinguish the
// parallel path from the sequential one.
func concurrencyTaskRegistry(active, maxObs *int32) *tools.Registry {
	r := tools.NewRegistry()
	r.MustRegister(tools.Tool{
		Name:        "task",
		Description: "delegates work to a sub-agent",
		Schema:      `{"type":"object","properties":{"prompt":{"type":"string"}}}`,
		Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
			n := atomic.AddInt32(active, 1)
			for {
				m := atomic.LoadInt32(maxObs)
				if n <= m || atomic.CompareAndSwapInt32(maxObs, m, n) {
					break
				}
			}
			time.Sleep(15 * time.Millisecond)
			atomic.AddInt32(active, -1)
			return tools.Result{Text: "ok"}, nil
		},
	})
	return r
}

// TestTaskParallel_ParallelRunsConcurrently: with TaskParallel=true
// (cloud default / auto-cloud), a batch of two task calls overlaps.
func TestTaskParallel_ParallelRunsConcurrently(t *testing.T) {
	var active, maxObs int32
	loop, err := NewLoop(LoopConfig{
		Provider:     &twoTaskCallProvider{name: "p"},
		Registry:     concurrencyTaskRegistry(&active, &maxObs),
		MaxSteps:     5,
		TaskParallel: true,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	drainEvents(t, mustRun(t, loop, "delegate two tasks"))
	if maxObs < 2 {
		t.Fatalf("max concurrent tasks = %d, want >=2 (parallel)", maxObs)
	}
}

// TestTaskParallel_SequentialRunsOneAtATime: with TaskParallel=false
// (local default / auto-local), the same batch never overlaps.
func TestTaskParallel_SequentialRunsOneAtATime(t *testing.T) {
	var active, maxObs int32
	loop, err := NewLoop(LoopConfig{
		Provider:     &twoTaskCallProvider{name: "p"},
		Registry:     concurrencyTaskRegistry(&active, &maxObs),
		MaxSteps:     5,
		TaskParallel: false,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	drainEvents(t, mustRun(t, loop, "delegate two tasks"))
	if maxObs != 1 {
		t.Fatalf("max concurrent tasks = %d, want 1 (sequential)", maxObs)
	}
}

func countNotices(evs []Event) int {
	n := 0
	for _, e := range evs {
		if _, ok := e.(NoticeEvent); ok {
			n++
		}
	}
	return n
}

// TestTaskParallel_WarnsWhenForcedLocal: forcing parallel on a local
// backend emits exactly one warning NoticeEvent when the batch runs.
func TestTaskParallel_WarnsWhenForcedLocal(t *testing.T) {
	var active, maxObs int32
	loop, err := NewLoop(LoopConfig{
		Provider:              &twoTaskCallProvider{name: "p"},
		Registry:              concurrencyTaskRegistry(&active, &maxObs),
		MaxSteps:              5,
		TaskParallel:          true,
		TaskParallelWarnLocal: true,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	evs := drainEvents(t, mustRun(t, loop, "delegate two tasks"))
	if got := countNotices(evs); got != 1 {
		t.Fatalf("warning notices = %d, want exactly 1", got)
	}
}

// TestTaskParallel_NoWarnWhenCloud: parallel on a cloud backend (no
// warn flag) runs concurrently without emitting a warning.
func TestTaskParallel_NoWarnWhenCloud(t *testing.T) {
	var active, maxObs int32
	loop, err := NewLoop(LoopConfig{
		Provider:              &twoTaskCallProvider{name: "p"},
		Registry:              concurrencyTaskRegistry(&active, &maxObs),
		MaxSteps:              5,
		TaskParallel:          true,
		TaskParallelWarnLocal: false,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	evs := drainEvents(t, mustRun(t, loop, "delegate two tasks"))
	if got := countNotices(evs); got != 0 {
		t.Fatalf("warning notices = %d, want 0 on cloud", got)
	}
}
