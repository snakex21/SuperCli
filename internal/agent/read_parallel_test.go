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

func TestReadOnlyToolBatchRunsConcurrently(t *testing.T) {
	var active, maxActive int32
	readFn := func(_ context.Context, _ json.RawMessage) (tools.Result, error) {
		n := atomic.AddInt32(&active, 1)
		for {
			old := atomic.LoadInt32(&maxActive)
			if n <= old || atomic.CompareAndSwapInt32(&maxActive, old, n) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		return tools.Result{Text: "ok"}, nil
	}
	reg := tools.NewRegistry()
	for _, name := range []string{"read_a", "read_b"} {
		reg.MustRegister(tools.Tool{Name: name, Description: "read", Schema: `{}`, ReadOnly: true, Fn: readFn})
	}
	loop, err := NewLoop(LoopConfig{Provider: echoProvider("ok"), Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan Event, 16)
	ok, outcomes := loop.invokeToolCalls(context.Background(), []llm.ToolCall{
		{ID: "1", Name: "read_a", Arguments: `{}`},
		{ID: "2", Name: "read_b", Arguments: `{}`},
	}, out)
	if failures := countFailures(outcomes); !ok || failures != 0 {
		t.Fatalf("ok=%v failures=%d", ok, failures)
	}
	if got := atomic.LoadInt32(&maxActive); got < 2 {
		t.Fatalf("max concurrent read tools = %d, want >= 2", got)
	}
	if got := loop.Messages[len(loop.Messages)-2:]; got[0].ToolCallID != "1" || got[1].ToolCallID != "2" {
		t.Fatalf("result order = %+v", got)
	}
}

func TestIndependentFileWritesRunConcurrently(t *testing.T) {
	var active, maxActive int32
	fn := func(_ context.Context, _ json.RawMessage) (tools.Result, error) {
		n := atomic.AddInt32(&active, 1)
		for {
			old := atomic.LoadInt32(&maxActive)
			if n <= old || atomic.CompareAndSwapInt32(&maxActive, old, n) {
				break
			}
		}
		time.Sleep(35 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		return tools.Result{Text: "ok"}, nil
	}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "write_file", Description: "write", Schema: `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`, Fn: fn, Verify: func(tools.Result) tools.VerifyVerdict { return tools.VerifyVerdict{OK: true} }})
	loop, _ := NewLoop(LoopConfig{Provider: echoProvider("ok"), Registry: reg})
	out := make(chan Event, 16)
	ok, outcomes := loop.invokeToolCalls(context.Background(), []llm.ToolCall{
		{ID: "1", Name: "write_file", Arguments: `{"path":"a.txt"}`},
		{ID: "2", Name: "write_file", Arguments: `{"path":"b.txt"}`},
	}, out)
	if failures := countFailures(outcomes); !ok || failures != 0 {
		t.Fatalf("ok=%v failures=%d", ok, failures)
	}
	if got := atomic.LoadInt32(&maxActive); got < 2 {
		t.Fatalf("max concurrent independent writes = %d, want >= 2", got)
	}
}

func TestSameFileReadWriteStaysSequential(t *testing.T) {
	var active, maxActive int32
	fn := func(_ context.Context, _ json.RawMessage) (tools.Result, error) {
		n := atomic.AddInt32(&active, 1)
		if n > atomic.LoadInt32(&maxActive) {
			atomic.StoreInt32(&maxActive, n)
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		return tools.Result{Text: "ok"}, nil
	}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "read_lines", Description: "read", Schema: `{"type":"object","properties":{"file":{"type":"string"}},"required":["file"]}`, ReadOnly: true, Fn: fn, Verify: func(tools.Result) tools.VerifyVerdict { return tools.VerifyVerdict{OK: true} }})
	reg.MustRegister(tools.Tool{Name: "patch_file", Description: "write", Schema: `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`, Fn: fn, Verify: func(tools.Result) tools.VerifyVerdict { return tools.VerifyVerdict{OK: true} }})
	loop, _ := NewLoop(LoopConfig{Provider: echoProvider("ok"), Registry: reg})
	out := make(chan Event, 16)
	ok, _ := loop.invokeToolCalls(context.Background(), []llm.ToolCall{
		{ID: "1", Name: "read_lines", Arguments: `{"file":"same.txt"}`},
		{ID: "2", Name: "patch_file", Arguments: `{"path":"same.txt"}`},
	}, out)
	if !ok {
		t.Fatal("same-file batch failed")
	}
	if got := atomic.LoadInt32(&maxActive); got != 1 {
		t.Fatalf("max concurrent same-file tools = %d, want 1", got)
	}
}

func TestMixedToolBatchStaysSequential(t *testing.T) {
	var active, maxActive int32
	fn := func(_ context.Context, _ json.RawMessage) (tools.Result, error) {
		n := atomic.AddInt32(&active, 1)
		if n > atomic.LoadInt32(&maxActive) {
			atomic.StoreInt32(&maxActive, n)
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		return tools.Result{Text: "ok"}, nil
	}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "read", Description: "read", Schema: `{}`, ReadOnly: true, Fn: fn})
	reg.MustRegister(tools.Tool{Name: "write", Description: "write", Schema: `{}`, Fn: fn})
	loop, _ := NewLoop(LoopConfig{Provider: echoProvider("ok"), Registry: reg})
	out := make(chan Event, 16)
	ok, _ := loop.invokeToolCalls(context.Background(), []llm.ToolCall{
		{ID: "1", Name: "read", Arguments: `{}`},
		{ID: "2", Name: "write", Arguments: `{}`},
	}, out)
	if !ok {
		t.Fatal("mixed batch failed")
	}
	if got := atomic.LoadInt32(&maxActive); got != 1 {
		t.Fatalf("max concurrent mixed tools = %d, want 1", got)
	}
}
