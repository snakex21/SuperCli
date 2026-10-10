package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Run this unchanged against the frozen pre-edit tool_result_reuse.go overlay
// as well as the candidate. The dispatch scenario measures the actual Fn count;
// cache-hit cases separately expose the bounded recency bookkeeping cost.
func BenchmarkToolResultReuseRecentHit(b *testing.B) {
	for _, rotating := range []bool{false, true} {
		name := "most-recent"
		if rotating {
			name = "rotating-16"
		}
		b.Run(name, func(b *testing.B) {
			var cache toolResultReuse
			now := time.Now()
			clock := func() time.Time { return now }
			ctx := context.Background()
			keys := make([][sha256.Size]byte, resultReuseEntries)
			for i := range keys {
				keys[i] = sha256.Sum256([]byte(fmt.Sprint(i)))
				_, _, _, claim, _ := cache.acquire(ctx, keys[i], false, clock)
				value := tools.Result{Text: "immutable verified observation"}
				cache.finish(claim, &value, time.Minute, now)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				index := len(keys) - 1
				if rotating {
					index = i % len(keys)
				}
				if _, _, hit, _, err := cache.acquire(ctx, keys[index], false, clock); !hit || err != nil {
					b.Fatal("fixture observation disappeared")
				}
			}
		})
	}
	b.Run("pressure-dispatch", func(b *testing.B) {
		calls := 0
		reg := tools.NewRegistry()
		reg.MustRegister(tools.Tool{Name: "catalog_lookup", Description: "trusted bounded lookup", ReadOnly: true,
			ReuseTTL: time.Minute, RefreshArg: "refresh", Schema: reuseTestSchema,
			Fn: func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
				calls++
				var args struct{ Query string }
				_ = json.Unmarshal(raw, &args)
				return tools.Result{Text: "Verified value: " + args.Query}, nil
			}})
		loop, err := NewLoop(LoopConfig{Provider: echoProvider("arbitrary-provider"), Registry: reg, System: "fixture"})
		if err != nil {
			b.Fatal(err)
		}
		makeCall := func(query string) llm.ToolCall {
			raw, _ := json.Marshal(map[string]string{"query": query})
			return llm.ToolCall{ID: query, Name: "catalog_lookup", Arguments: string(raw)}
		}
		sequence := []llm.ToolCall{makeCall("active")}
		for i := 1; i < resultReuseEntries; i++ {
			sequence = append(sequence, makeCall(fmt.Sprint("other-", i)))
		}
		sequence = append(sequence, makeCall("active"), makeCall("pressure"), makeCall("active"))
		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			loop.resultReuse.reset()
			out := make(chan Event, len(sequence)*4)
			for _, call := range sequence {
				if got := loop.invoke(ctx, call, out); got.failed {
					b.Fatal("valid lookup failed")
				}
			}
		}
		b.StopTimer()
		b.ReportMetric(float64(calls)/float64(b.N), "Fn/op")
	})
}
