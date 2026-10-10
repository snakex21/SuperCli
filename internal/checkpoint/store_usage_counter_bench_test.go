package checkpoint

import (
	"context"
	"testing"
)

// Measures the real post-turn gate/read/decision, rather than a mocked bool.
// Initialization/census and all portable fixture cleanup remain outside timing.
func BenchmarkStoreUsageCounterCleanCompletion(b *testing.B) {
	b.StopTimer()
	gate, err := NewStoreGate(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	c, err := NewStoreUsageCounter(gate)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	lease, err := gate.Acquire(ctx)
	if err != nil {
		b.Fatal(err)
	}
	_, firstErr := c.CompleteLocked(ctx, DefaultStoreBudgetBytes, func(context.Context) (int64, error) { return 0, nil })
	closeErr := lease.Close()
	if firstErr != nil || closeErr != nil {
		b.Fatalf("initialize: %v %v", firstErr, closeErr)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		lease, err := gate.Acquire(ctx)
		if err != nil {
			b.Fatal(err)
		}
		result, readErr := c.CompleteLocked(ctx, DefaultStoreBudgetBytes, nil)
		closeErr := lease.Close()
		if readErr != nil || closeErr != nil || result.Collected {
			b.Fatalf("fastpath: %+v %v %v", result, readErr, closeErr)
		}
	}
	b.StopTimer()
	b.ReportMetric(0, "census/op")
	b.ReportMetric(0, "writes/op")
	b.ReportMetric(0, "gitcalls/op")
}

// Includes the durable dirty marker and final upper-bound publication so
// accounting overhead is visible alongside the read-only completion fastpath.
func BenchmarkStoreUsageCounterWriteReceipt(b *testing.B) {
	b.StopTimer()
	gate, err := NewStoreGate(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	counter, err := NewStoreUsageCounter(gate)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	lease, err := gate.Acquire(ctx)
	if err != nil {
		b.Fatal(err)
	}
	_, firstErr := counter.CompleteLocked(ctx, DefaultStoreBudgetBytes, func(context.Context) (int64, error) { return 0, nil })
	if closeErr := lease.Close(); firstErr != nil || closeErr != nil {
		b.Fatalf("initialize: %v %v", firstErr, closeErr)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		lease, err := gate.Acquire(ctx)
		if err != nil {
			b.Fatal(err)
		}
		receipt, writeErr := counter.BeginLocked(ctx)
		if writeErr == nil {
			writeErr = receipt.FinishLocked(ctx, StoreUsageGrowth{MetadataBytes: 64})
		}
		closeErr := lease.Close()
		if writeErr != nil || closeErr != nil {
			b.Fatalf("write receipt: %v %v", writeErr, closeErr)
		}
	}
	b.StopTimer()
	b.ReportMetric(2, "writes/op")
	b.ReportMetric(0, "census/op")
	b.ReportMetric(0, "gitcalls/op")
}
