//go:build benchmark

// EvictForBudget scaling benchmarks. Run with:
//
//	go test -tags benchmark -bench BenchmarkEvictForBudget -benchmem ./test/
//
// Pins the single-pass rewrite (2026-07-12): the old form re-ran
// EstimateVisibleTokens() (O(n)) on every eviction, making a mass
// evict O(n²) in history length. Baseline lives in
// docs/performance.md — re-run and compare after touching hide.go.
package test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

// benchEvictProvider is never actually called — EvictForBudget does
// no provider I/O — but NewLoop requires a provider.
type benchEvictProvider struct{}

func (benchEvictProvider) Name() string { return "bench-evict" }
func (benchEvictProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	ch := make(chan llm.Delta, 1)
	ch <- llm.Delta{FinishReason: "stop"}
	close(ch)
	return ch, nil
}

// benchEvictCredit exposes a tiny session cap so nearly the whole
// history has to be evicted (the mass-evict worst case).
type benchEvictCredit struct{ cap int64 }

func (c *benchEvictCredit) SessionCap() int64 { return c.cap }
func (c *benchEvictCredit) Record(_ context.Context, _, _ int64, _ string) error {
	return nil
}
func (c *benchEvictCredit) Used() (session, daily int64) { return 0, 0 }

func benchmarkEvictForBudget(b *testing.B, n int) {
	body := strings.Repeat("word ", 40) // ~200 bytes per message
	msgs := make([]llm.Message, n)
	for i := range msgs {
		role := llm.RoleUser
		if i%2 == 1 {
			role = llm.RoleAssistant
		}
		msgs[i] = llm.Message{Role: role, Content: fmt.Sprintf("m%d %s", i, body)}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		l, err := agent.NewLoop(agent.LoopConfig{
			Provider:      benchEvictProvider{},
			Registry:      tools.NewRegistry(),
			System:        "bench",
			CreditTracker: &benchEvictCredit{cap: 500}, // threshold 400 → mass evict
		})
		if err != nil {
			b.Fatalf("NewLoop: %v", err)
		}
		l.Messages = append(l.Messages, msgs...)
		b.StartTimer()
		if evicted := l.EvictForBudget(context.Background(), nil); evicted < n/2 {
			b.Fatalf("evicted %d of %d, expected a mass evict", evicted, n)
		}
	}
}

func BenchmarkEvictForBudget_1kMsgs(b *testing.B)  { benchmarkEvictForBudget(b, 1000) }
func BenchmarkEvictForBudget_10kMsgs(b *testing.B) { benchmarkEvictForBudget(b, 10000) }
