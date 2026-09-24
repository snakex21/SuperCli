package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func observed(call llm.ToolCall, text string) callOutcome {
	return callOutcome{observation: observeToolResult(call, tools.Result{Text: text})}
}

func TestObservedLoopDetectsLongCycleWithoutStopping(t *testing.T) {
	var p repeatProgress
	var calls []llm.ToolCall
	for i := 0; i < 12; i++ {
		call := llm.ToolCall{Name: "read_lines", Arguments: fmt.Sprintf(`{"file":"f%d.go"}`, i)}
		calls = append(calls, call)
		if got := p.observe([]llm.ToolCall{call}, []callOutcome{observed(call, fmt.Sprintf("file %d", i))}); got != repeatNone {
			t.Fatalf("novel read triggered %v", got)
		}
	}
	warned := false
	for i := 0; i < len(calls)*2; i++ {
		index := i % len(calls)
		call := calls[index]
		got := p.observe([]llm.ToolCall{call}, []callOutcome{observed(call, fmt.Sprintf("file %d", index))})
		warned = warned || got == repeatWarn
		if got == repeatAbort {
			t.Fatalf("observation cycle prematurely stopped at %d", i)
		}
	}
	if !warned {
		t.Fatal("long read loop escaped detection")
	}
}

func TestObservedLoopAllowsNewResultsAndNewFiles(t *testing.T) {
	for _, changingArgs := range []bool{false, true} {
		var p repeatProgress
		for i := 0; i < 100; i++ {
			call := llm.ToolCall{Name: "read_lines", Arguments: `{"file":"changing.log"}`}
			body := fmt.Sprintf("version %d", i)
			if changingArgs {
				call.Arguments = fmt.Sprintf(`{"file":"%d.go"}`, i)
				body = "same content in a different file"
			}
			if got := p.observe([]llm.ToolCall{call}, []callOutcome{observed(call, body)}); got != repeatNone {
				t.Fatalf("legitimate discovery stopped/warned: changingArgs=%v i=%d signal=%v", changingArgs, i, got)
			}
		}
	}
}

func TestObservedLoopResetsAfterMutationFailureAndUnknownCommand(t *testing.T) {
	read := llm.ToolCall{Name: "read_lines", Arguments: `{"file":"f.go"}`}
	for _, barrier := range []struct {
		call    llm.ToolCall
		outcome callOutcome
	}{
		{llm.ToolCall{Name: "patch_file", Arguments: "{}"}, callOutcome{}},
		{llm.ToolCall{Name: "task", Arguments: "{}"}, callOutcome{}},
		{llm.ToolCall{Name: "ctx_execute", Arguments: `{"command":["powershell","-Command","Get-Process"]}`}, callOutcome{}},
		{read, callOutcome{failed: true}},
	} {
		var p repeatProgress
		for i := 0; i < 4; i++ {
			p.observe([]llm.ToolCall{read}, []callOutcome{observed(read, "unchanged")})
		}
		p.observe([]llm.ToolCall{barrier.call}, []callOutcome{barrier.outcome})
		if p.unchanged.rounds != 0 || len(p.unchanged.seen) != 0 {
			t.Fatal("barrier retained stale evidence")
		}
		p.observe([]llm.ToolCall{read}, []callOutcome{observed(read, "unchanged")})
		if p.unchanged.rounds != 0 {
			t.Fatal("verification after mutation treated as redundant")
		}
	}
}

func TestTestResultObservationIgnoresTimingButPreservesEvidence(t *testing.T) {
	call := llm.ToolCall{Name: "ctx_execute", Arguments: `{"command":["python","tools/tests/test_selection.py","-v"]}`}
	a := observed(call, `{"stdout":"","stderr":"Ran 5 tests in 2.941s\r\n\r\nOK\r\n","exit_code":0,"duration_ms":3000,"truncated_stdout":false}`)
	b := observed(call, `{"duration_ms":1800,"exit_code":0,"stdout":"","stderr":"Ran 5 tests in 1.683s\r\n\r\nOK\r\n","truncated_stdout":false}`)
	if !a.observation.valid || a.observation.result != b.observation.result {
		t.Fatal("elapsed time hid identical successful test result")
	}
	c := observed(call, `{"stdout":"new evidence","stderr":"Ran 5 tests in 1.683s\r\n\r\nOK\r\n","exit_code":0,"duration_ms":1800,"truncated_stdout":false}`)
	if a.observation.result == c.observation.result {
		t.Fatal("substantive output change lost")
	}
	failed := observed(call, `{"exit_code":1,"stdout":"","stderr":"FAILED","duration_ms":100}`)
	if failed.observation.valid {
		t.Fatal("failure counted as unchanged successful verification")
	}
}

func TestObservedHistoryIsBounded(t *testing.T) {
	var p repeatProgress
	for i := 0; i < 1000; i++ {
		c := llm.ToolCall{Name: "list_dir", Arguments: fmt.Sprintf(`{"path":"dir%d"}`, i)}
		p.observe([]llm.ToolCall{c}, []callOutcome{observed(c, "listing")})
	}
	if len(p.unchanged.seen) > observationHistoryLimit || len(p.unchanged.order) > observationHistoryLimit {
		t.Fatal("unbounded state")
	}
}

func TestLoopUnchangedReadsCanRecoverAfterSixRounds(t *testing.T) {
	p := &stubProvider{name: "observation-test"}
	for i := 0; i < 12; i++ {
		p.scripts = append(p.scripts, []llm.Delta{
			{ToolCall: &llm.ToolCall{ID: fmt.Sprint(i), Name: "read_lines", Arguments: `{"file":"a"}`}},
			{FinishReason: "tool_calls"},
		})
	}
	p.scripts = append(p.scripts, []llm.Delta{{Content: "Finished.", FinishReason: "stop"}})
	reg := tools.NewRegistry()
	var executions int
	reg.MustRegister(tools.Tool{Name: "read_lines", Description: "read", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			executions++
			return tools.Result{Text: "same lines"}, nil
		}})
	reg.MarkAlwaysOn("read_lines")
	l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, ThinTools: false})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := l.Run(context.Background(), "read")
	if err != nil {
		t.Fatal(err)
	}
	events := drainEvents(t, ch)
	for _, ev := range events {
		if e, ok := ev.(ErrorEvent); ok {
			t.Fatalf("recovery interrupted: %v", e.Err)
		}
	}
	if _, ok := events[len(events)-1].(DoneEvent); !ok {
		t.Fatal("no final answer")
	}
	if executions != 12 || int(p.calls) != executions+1 {
		t.Fatalf("unexpected extra requests: executions=%d calls=%d", executions, p.calls)
	}
}

func TestLoopUserSteeringResetsUnchangedProgress(t *testing.T) {
	read := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "x", Name: "read_lines", Arguments: `{"file":"a"}`}}, {FinishReason: "tool_calls"}}
	p := &stubProvider{name: "steering"}
	for i := 0; i < 8; i++ {
		p.scripts = append(p.scripts, read)
	}
	p.scripts = append(p.scripts, []llm.Delta{{Content: "Finished.", FinishReason: "stop"}})
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "read_lines", Description: "read", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "same lines"}, nil
		}})
	reg.MarkAlwaysOn("read_lines")
	l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, ThinTools: false})
	if err != nil {
		t.Fatal(err)
	}
	p.onCalled = func(n int) {
		if n == 6 && !l.QueueInterjection("Repeat the verification now.") {
			t.Error("steering rejected")
		}
	}
	ch, err := l.Run(context.Background(), "read")
	if err != nil {
		t.Fatal(err)
	}
	events := drainEvents(t, ch)
	for _, event := range events {
		if e, ok := event.(ErrorEvent); ok {
			t.Fatalf("user steering did not reset progress: %v", e.Err)
		}
	}
	if _, ok := events[len(events)-1].(DoneEvent); !ok {
		t.Fatal("no final answer")
	}
}

func TestLargeReadObservationUsesFullOutputBeforeHandleCompaction(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "read_lines", Description: "read", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: strings.Repeat("same file line\n", 2000)}, nil
		}})
	l, err := NewLoop(LoopConfig{Provider: &stubProvider{}, Registry: reg, ThinTools: false})
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan Event, 8)
	call := llm.ToolCall{ID: "a", Name: "read_lines", Arguments: `{"file":"a"}`}
	first := l.invoke(context.Background(), call, out)
	call.ID = "b"
	second := l.invoke(context.Background(), call, out)
	if !first.observation.valid || first.observation.result != second.observation.result {
		t.Fatal("output handle hid a repeated large read")
	}
}

func TestObservedReadsDoNotPayForRedundantAdaptiveReflection(t *testing.T) {
	for _, changing := range []bool{false, true} {
		t.Run(fmt.Sprintf("changing=%v", changing), func(t *testing.T) {
			p := &stubProvider{name: "observed"}
			for i := 0; i < 4; i++ {
				p.scripts = append(p.scripts, []llm.Delta{{ToolCall: &llm.ToolCall{ID: fmt.Sprint(i), Name: "read_lines", Arguments: `{"file":"a"}`}}})
			}
			p.scripts = append(p.scripts, []llm.Delta{{Content: "Finished.", FinishReason: "stop"}})
			reg := tools.NewRegistry()
			version := 0
			reg.MustRegister(tools.Tool{Name: "read_lines", Description: "read", Schema: `{"type":"object"}`,
				Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
					if changing {
						version++
					}
					return tools.Result{Text: fmt.Sprint(version)}, nil
				},
			})
			reg.MarkAlwaysOn("read_lines")
			reflector := &stubReflector{text: "unnecessary model call"}
			l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, Reflector: reflector, AdaptiveReflection: true})
			if err != nil {
				t.Fatal(err)
			}
			ch, err := l.Run(context.Background(), "Inspect.")
			if err != nil {
				t.Fatal(err)
			}
			events := drainEvents(t, ch)
			for _, ev := range events {
				if e, ok := ev.(ErrorEvent); ok {
					t.Fatal(e.Err)
				}
			}
			if reflector.calls != 0 || p.calls != 5 {
				t.Fatalf("reflections=%d provider calls=%d", reflector.calls, p.calls)
			}
		})
	}
}
