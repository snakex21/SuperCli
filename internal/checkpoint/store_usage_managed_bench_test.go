package checkpoint

import (
	"context"
	"testing"
)

// Gate acquisition and the real bounded ledger read are measured together.
// The synthetic complete-census setup is outside timing; no graph is cached.
func BenchmarkStoreManagedUsageProtectedFloorCleanCompletion(b *testing.B) {
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
	_, initErr := counter.CompleteManagedLocked(ctx, DefaultStoreBudgetBytes, func(context.Context) (StoreBudgetResult, error) {
		return managedUsageMeasurement(3*DefaultStoreBudgetBytes, 2*DefaultStoreBudgetBytes), nil
	})
	if closeErr := lease.Close(); initErr != nil || closeErr != nil {
		b.Fatalf("initialize: %v %v", initErr, closeErr)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		lease, err := gate.Acquire(ctx)
		if err != nil {
			b.Fatal(err)
		}
		result, readErr := counter.CompleteManagedLocked(ctx, DefaultStoreBudgetBytes, nil)
		closeErr := lease.Close()
		if readErr != nil || closeErr != nil || result.Collected || result.EffectiveLimitBytes != 3*DefaultStoreBudgetBytes {
			b.Fatalf("fastpath: %+v %v %v", result, readErr, closeErr)
		}
	}
	b.StopTimer()
	b.ReportMetric(0, "census/op")
	b.ReportMetric(0, "writes/op")
	b.ReportMetric(0, "gitcalls/op")
}
