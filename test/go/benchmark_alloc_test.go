//go:build benchmark

// Allocation/memory benchmarks for the agent hot paths. Run with:
//
//	go test -tags benchmark -bench . -benchmem ./test/
//
// A baseline (date-stamped) lives in docs/performance.md — re-run and
// compare (benchstat) after touching providerMessages/consume/prune,
// the tool dispatch path, core.HeadTailBuffer, or the worker registry.
package test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

// benchScriptProvider replays one scripted delta slice per Complete
// call, clamping to the last script (same contract as the agent
// package's test stub).
type benchScriptProvider struct {
	scripts [][]llm.Delta
	calls   int
}

func (p *benchScriptProvider) Name() string { return "bench-script" }

func (p *benchScriptProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	idx := p.calls
	p.calls++
	if idx >= len(p.scripts) {
		idx = len(p.scripts) - 1
	}
	script := p.scripts[idx]
	ch := make(chan llm.Delta, 256)
	go func() {
		defer close(ch)
		for _, d := range script {
			select {
			case ch <- d:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// benchStreamProvider streams n small content deltas then stops —
// the consume() large-stream workload.
type benchStreamProvider struct{ n int }

func (p *benchStreamProvider) Name() string { return "bench-stream" }

func (p *benchStreamProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta, 256)
	go func() {
		defer close(ch)
		for i := 0; i < p.n; i++ {
			select {
			case ch <- llm.Delta{Content: "token town "}:
			case <-ctx.Done():
				return
			}
		}
		ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 10, Output: p.n}}
	}()
	return ch, nil
}

func stopScript() []llm.Delta {
	return []llm.Delta{
		{Role: llm.RoleAssistant, Content: "ok"},
		{FinishReason: "stop", Usage: &llm.Usage{Input: 10, Output: 1, Total: 11}},
	}
}

// buildBenchHistory produces a realistic mixed history: user turns,
// assistant tool calls, tool results, assistant answers.
func buildBenchHistory(n int) []llm.Message {
	msgs := make([]llm.Message, 0, n)
	for i := 0; len(msgs) < n; i++ {
		id := fmt.Sprintf("call_%d", i)
		msgs = append(msgs,
			llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("please inspect item %d and summarize what it does in the project", i)},
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read_lines", Arguments: fmt.Sprintf(`{"file":"item%d.go","from":1,"to":40}`, i)}}},
			llm.Message{Role: llm.RoleTool, ToolCallID: id, Name: "read_lines", Content: fmt.Sprintf("item %d contents: package main // lines 1-40 of a plausible source file with some text payload", i)},
			llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("Item %d is a helper; it wires the registry and returns a result.", i)},
		)
	}
	return msgs[:n]
}

// BenchmarkLongSessionPrepare measures a full Run over a LONG
// pre-existing history with an instant provider: the cost is
// dominated by the per-step context preparation (visible view,
// provider message assembly, token estimation). Window is huge so
// prune/compaction never fire — this pins pure prepare cost.
func BenchmarkLongSessionPrepare(b *testing.B) {
	for _, n := range []int{100, 500, 2000} {
		hist := buildBenchHistory(n)
		b.Run(fmt.Sprintf("msgs=%d", n), func(b *testing.B) {
			reg := tools.NewRegistry()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p := &benchScriptProvider{scripts: [][]llm.Delta{stopScript()}}
				loop, err := agent.NewLoop(agent.LoopConfig{
					Provider:        p,
					Registry:        reg,
					MaxSteps:        2,
					InitialMessages: hist,
					WindowFor:       func(string) int { return 1 << 20 },
				})
				if err != nil {
					b.Fatalf("NewLoop: %v", err)
				}
				ch, _ := loop.Run(context.Background(), "next question")
				for range ch {
				}
			}
		})
	}
}

// BenchmarkConsumeLargeStream pins the streaming consume() path at
// 1k/10k/100k deltas. After the 593a352 fix (incremental marker
// scanner instead of quadratic text += delta) this must stay ~O(n);
// a superlinear regression shows up immediately between sizes.
func BenchmarkConsumeLargeStream(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("deltas=%d", n), func(b *testing.B) {
			reg := tools.NewRegistry()
			b.ReportAllocs()
			b.SetBytes(int64(n * len("token town ")))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				loop, err := agent.NewLoop(agent.LoopConfig{
					Provider: &benchStreamProvider{n: n},
					Registry: reg,
					MaxSteps: 2,
				})
				if err != nil {
					b.Fatalf("NewLoop: %v", err)
				}
				ch, _ := loop.Run(context.Background(), "stream it")
				for range ch {
				}
			}
		})
	}
}

// BenchmarkToolBatch measures one model step that fans out into K
// tool calls (sequential dispatch through Registry.Execute plus
// history append of K results).
func BenchmarkToolBatch(b *testing.B) {
	for _, k := range []int{4, 16, 64} {
		b.Run(fmt.Sprintf("calls=%d", k), func(b *testing.B) {
			reg := tools.NewRegistry()
			reg.MustRegister(tools.Tool{
				Name:        "bench_tool",
				Description: "cheap benchmark tool",
				Schema:      `{"type":"object","properties":{"i":{"type":"integer"}}}`,
				Fn: func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
					return tools.Result{Text: "ok"}, nil
				},
			})
			step1 := []llm.Delta{{Role: llm.RoleAssistant}}
			for i := 0; i < k; i++ {
				step1 = append(step1, llm.Delta{ToolCall: &llm.ToolCall{
					ID: fmt.Sprintf("c%d", i), Name: "bench_tool", Arguments: fmt.Sprintf(`{"i":%d}`, i),
				}})
			}
			step1 = append(step1, llm.Delta{FinishReason: "tool_calls"})
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p := &benchScriptProvider{scripts: [][]llm.Delta{step1, stopScript()}}
				loop, err := agent.NewLoop(agent.LoopConfig{Provider: p, Registry: reg, MaxSteps: 3})
				if err != nil {
					b.Fatalf("NewLoop: %v", err)
				}
				ch, _ := loop.Run(context.Background(), "fan out")
				for range ch {
				}
			}
		})
	}
}

// BenchmarkContextReport measures the /context accounting over a
// large history (token estimation per message + report formatting).
func BenchmarkContextReport(b *testing.B) {
	for _, n := range []int{200, 1000} {
		b.Run(fmt.Sprintf("msgs=%d", n), func(b *testing.B) {
			reg := tools.NewRegistry()
			loop, err := agent.NewLoop(agent.LoopConfig{
				Provider:        &benchScriptProvider{scripts: [][]llm.Delta{stopScript()}},
				Registry:        reg,
				InitialMessages: buildBenchHistory(n),
				System:          "You are SuperCli.",
			})
			if err != nil {
				b.Fatalf("NewLoop: %v", err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r := loop.ContextReport()
				if s := agent.FormatContextReport(r); s == "" {
					b.Fatal("empty report")
				}
			}
		})
	}
}

// BenchmarkHeadTailBuffer streams a large process output through
// core.HeadTailBuffer (8 KB head + 8 KB tail caps, 8 KB writes) and
// renders it — memory must stay bounded regardless of total size.
func BenchmarkHeadTailBuffer(b *testing.B) {
	chunk := make([]byte, 8192)
	for i := range chunk {
		chunk[i] = byte('a' + i%26)
	}
	for _, total := range []int{1 << 20, 16 << 20, 64 << 20} {
		b.Run(fmt.Sprintf("total=%dMB", total>>20), func(b *testing.B) {
			writes := total / len(chunk)
			b.ReportAllocs()
			b.SetBytes(int64(total))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf := core.NewHeadTailBuffer(8192, 8192)
				for w := 0; w < writes; w++ {
					_, _ = buf.Write(chunk)
				}
				if s := buf.String(); len(s) == 0 {
					b.Fatal("empty render")
				}
			}
		})
	}
}

// BenchmarkWorkerRegistry pins the 29194ae retention machinery on a
// registry FULL of finished workers: every Add over the retention cap
// runs the sweep (status scan + LRU sort + eviction snapshot), and
// Counts/List are the TUI-path reads.
func BenchmarkWorkerRegistry(b *testing.B) {
	newFullRegistry := func(b *testing.B, finished int) (*agent.WorkerRegistry, *agent.Loop) {
		b.Helper()
		// Retention = finished so the prefill is retained in full and
		// every benchmarked Add sweeps a registry of that size.
		b.Setenv("SUPERCLI_WORKER_RETENTION", fmt.Sprint(finished))
		loop, err := agent.NewLoop(agent.LoopConfig{
			Provider: &benchScriptProvider{scripts: [][]llm.Delta{stopScript()}},
			Registry: tools.NewRegistry(),
		})
		if err != nil {
			b.Fatalf("NewLoop: %v", err)
		}
		reg := agent.NewWorkerRegistry()
		for i := 0; i < finished; i++ {
			w := reg.Add("general", fmt.Sprintf("prefill %d", i), loop)
			w.Status = "done"
			w.LastResult = "finished the prefill task with a short report"
		}
		return reg, loop
	}

	for _, finished := range []int{100, 1000} {
		b.Run(fmt.Sprintf("add_sweep/finished=%d", finished), func(b *testing.B) {
			reg, loop := newFullRegistry(b, finished)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				w := reg.Add("general", "bench task", loop)
				w.Status = "done"
			}
		})
		b.Run(fmt.Sprintf("counts/finished=%d", finished), func(b *testing.B) {
			reg, _ := newFullRegistry(b, finished)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if c := reg.Counts(); c.Total == 0 {
					b.Fatal("empty registry")
				}
			}
		})
		b.Run(fmt.Sprintf("list/finished=%d", finished), func(b *testing.B) {
			reg, _ := newFullRegistry(b, finished)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if ws := reg.List(); len(ws) == 0 {
					b.Fatal("empty list")
				}
			}
		})
	}
}
