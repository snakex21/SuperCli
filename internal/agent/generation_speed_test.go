package agent

import (
	"context"
	"encoding/json"
	"errors"
	"supercli/internal/llm"
	"supercli/internal/tools"
	"testing"
	"time"
)

func TestGenerationRateUsesMeasuredOutputWindow(t *testing.T) {
	for _, tc := range []struct {
		tokens   int
		duration time.Duration
		want     float64
	}{{120, 3 * time.Second, 40}, {0, time.Second, 0}, {10, 0, 0}, {10, -time.Second, 0}, {10, time.Microsecond, 0}} {
		e := DoneEvent{GenerationTokens: tc.tokens, GenerationDuration: tc.duration}
		if got := e.GenerationTokensPerSecond(); got != tc.want {
			t.Fatalf("rate=%v want=%v", got, tc.want)
		}
	}
}

type generationWindowProvider struct {
	calls   int
	windows []time.Duration
}

func (p *generationWindowProvider) Name() string { return "owned-generation-window" }
func (p *generationWindowProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	call := p.calls
	ch := make(chan llm.Delta)
	go func() {
		defer close(ch)
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			return
		}
		first := time.Now()
		if call == 1 {
			ch <- llm.Delta{Reasoning: "fixture reasoning"}
		} else {
			ch <- llm.Delta{Content: "fixture reply"}
		}
		time.Sleep(3 * time.Millisecond)
		if call == 1 {
			ch <- llm.Delta{ToolCall: &llm.ToolCall{ID: "fixture-call", Name: "fixture", Arguments: "{}"}}
		}
		if call <= 2 {
			out := 30
			if call == 2 {
				out = 90
			}
			ch <- llm.Delta{Usage: &llm.Usage{Input: 100, Output: out, Total: 100 + out, Reasoning: 10}, FinishReason: "stop"}
		}
		p.windows = append(p.windows, time.Since(first))
	}()
	return ch, nil
}
func TestLoopGenerationRateWeightsCallsAndResetsAcrossRuns(t *testing.T) {
	p := &generationWindowProvider{}
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "fixture", Description: "owned fixture", ReadOnly: true, Schema: `{"type":"object"}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		time.Sleep(5 * time.Millisecond)
		return tools.Result{Text: "fixture read complete"}, nil
	}, Verify: func(tools.Result) tools.VerifyVerdict { return tools.VerifyVerdict{OK: true} }})
	reg.Activate("fixture")
	l, err := NewLoop(LoopConfig{Provider: p, Registry: reg, System: "owned test", MaxSteps: 4, SkipImplementationHint: true})
	if err != nil {
		t.Fatal(err)
	}
	run := func() DoneEvent {
		ch, err := l.Run(context.Background(), "run fixture")
		if err != nil {
			t.Fatal(err)
		}
		var done *DoneEvent
		for ev := range ch {
			switch e := ev.(type) {
			case DoneEvent:
				done = &e
			case ErrorEvent:
				t.Fatal(e.Err)
			}
		}
		if done == nil {
			t.Fatal("no completion")
		}
		return *done
	}
	done := run()
	if done.GenerationTokens != 120 || done.Usage.Output != 120 || done.GenerationDuration <= 0 {
		t.Fatalf("generation=%+v", done)
	}
	if done.GenerationTokensPerSecond() != 120/done.GenerationDuration.Seconds() {
		t.Fatal("not weighted over matching calls")
	}
	if l.lastCallTTFT < 5*time.Millisecond {
		t.Fatal("fixture did not exercise prompt wait")
	}
	// The output-only timer excludes both known prompt waits and tool execution.
	observed := p.windows[0] + p.windows[1]
	if done.GenerationDuration > observed+10*time.Millisecond {
		t.Fatalf("included wait/tool time: rateWindow=%v actualOutput=%v", done.GenerationDuration, observed)
	}
	next := run()
	if next.GenerationTokens != 0 || next.GenerationDuration != 0 || next.GenerationTokensPerSecond() != 0 {
		t.Fatalf("usage-less next run inherited old rate: %+v", next)
	}
}
func TestFailedStreamDoesNotContributeGenerationRate(t *testing.T) {
	p := &stubProvider{name: "owned-failure", scripts: [][]llm.Delta{{{Content: "partial", Usage: &llm.Usage{Output: 99}}, {Err: errors.New("owned failure")}}}}
	l, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), System: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	events, err := l.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	if l.generationTokens != 0 || l.generationDuration != 0 {
		t.Fatal("failed output produced a rate")
	}
}
