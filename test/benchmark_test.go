//go:build benchmark

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

type benchmarkEcho struct{}

func (b *benchmarkEcho) Name() string        { return "bench-echo" }
func (b *benchmarkEcho) SupportsVision() bool { return false }
func (b *benchmarkEcho) Complete(ctx context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta, 128)
	go func() {
		defer close(ch)
		const tokens = 500
		for i := 0; i < tokens; i++ {
			select {
			case <-ctx.Done():
				return
			case ch <- llm.Delta{Content: fmt.Sprintf("token%d ", i)}:
			}
		}
		ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 50, Output: tokens}}
	}()
	return ch, nil
}

// BenchmarkTokenThroughput measures tokens/s through the full
// agent loop (provider → consume → events).
func BenchmarkTokenThroughput(b *testing.B) {
	p := &benchmarkEcho{}
	reg := tools.NewRegistry()
	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider: p,
		Registry: reg,
		MaxSteps: 2,
	})
	if err != nil {
		b.Fatalf("NewLoop: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		ch, _ := loop.Run(ctx, "bench")
		for range ch {
		}
		cancel()
	}
}

// BenchmarkToolDispatch measures the latency of a single
// tool call dispatch through the Registry.Execute path.
func BenchmarkToolDispatch(b *testing.B) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{
		Name:        "bench_tool",
		Description: "Benchmark tool.",
		Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "ok"}, nil
		},
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := reg.Execute(context.Background(), "bench_tool", json.RawMessage(`{}`))
		if err != nil {
			b.Fatalf("Execute: %v", err)
		}
	}
}
