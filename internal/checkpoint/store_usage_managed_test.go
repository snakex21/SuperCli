package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/system/childproc"
)

func managedUsageMeasurement(bytes, floor int64) StoreBudgetResult {
	return StoreBudgetResult{PhysicalComplete: true, FullCensusComplete: true, MeasuredBytes: bytes, ProtectedBytes: floor}
}

func TestStoreManagedUsageHundredCleanCompletionsHaveZeroCensus(t *testing.T) {
	gate, c := newUsageCounterFixture(t)
	withUsageCounterGate(t, gate, func() {
		ctx := context.Background()
		calls := 0
		collect := func(context.Context) (StoreBudgetResult, error) {
			calls++
			return managedUsageMeasurement(1100, 1024), nil
		}
		first, err := c.CompleteManagedLocked(ctx, 100, collect)
		if err != nil || !first.Collected || first.EffectiveLimitBytes != 1124 || first.ProtectedFloorBytes != 1024 || calls != 1 {
			t.Fatalf("seed proven floor: %+v calls=%d err=%v", first, calls, err)
		}
		for i := 0; i < 100; i++ {
			result, err := c.CompleteManagedLocked(ctx, 100, collect)
			if err != nil || result.Collected || result.UpperBytes != 1100 || result.ProtectedFloorBytes != 1024 || result.EffectiveLimitBytes != 1124 || calls != 1 {
				t.Fatalf("clean completion %d recounted: %+v calls=%d err=%v", i, result, calls, err)
			}
		}
		// Known write adds only bounded growth and preserves the scalar proof.
		w, err := c.BeginLocked(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.AddPublishedBlobBytes(10); err != nil {
			t.Fatal(err)
		}
		if err := w.FinishLocked(ctx, StoreUsageGrowth{}); err != nil {
			t.Fatal(err)
		}
		result, err := c.CompleteManagedLocked(ctx, 100, collect)
		if err != nil || result.Collected || result.UpperBytes != 1110 || calls != 1 {
			t.Fatalf("known bounded write forced census: %+v calls=%d err=%v", result, calls, err)
		}
	})
}

func TestStoreManagedUsagePressureAndDirtyRecomputeShrinkingFloor(t *testing.T) {
	gate, c := newUsageCounterFixture(t)
	withUsageCounterGate(t, gate, func() {
		ctx := context.Background()
		calls := 0
		current := managedUsageMeasurement(1100, 1024)
		collect := func(context.Context) (StoreBudgetResult, error) { calls++; return current, nil }
		if _, err := c.CompleteManagedLocked(ctx, 100, collect); err != nil {
			t.Fatal(err)
		}
		w, err := c.BeginLocked(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.AddPublishedBlobBytes(50); err != nil {
			t.Fatal(err)
		}
		if err := w.FinishLocked(ctx, StoreUsageGrowth{}); err != nil {
			t.Fatal(err)
		}
		current = managedUsageMeasurement(600, 550)
		result, err := c.CompleteManagedLocked(ctx, 100, collect)
		if err != nil || !result.Collected || result.ProtectedFloorBytes != 550 || result.EffectiveLimitBytes != 650 || calls != 2 {
			t.Fatalf("pressure did not recompute floor: %+v calls=%d err=%v", result, calls, err)
		}
		// Clear/Forget/unknown changes keep dirty sticky even after a subsequent
		// successful write, so the former allowance cannot be reused unchanged.
		w, err = c.BeginLocked(ctx)
		if err != nil || w.RequireCensus() != nil || w.FinishLocked(ctx, StoreUsageGrowth{}) != nil {
			t.Fatal("cannot invalidate managed allowance:", err)
		}
		current = StoreBudgetResult{PhysicalComplete: true, MeasuredBytes: 40}
		result, err = c.CompleteManagedLocked(ctx, 100, collect)
		state, stateErr := c.readLocked()
		if err != nil || stateErr != nil || !result.Collected || result.ProtectedFloorBytes != 0 || result.EffectiveLimitBytes != 100 || state.ProtectedFloor != 0 || state.Dirty || calls != 3 {
			t.Fatalf("old floor survived fresh small total: %+v state=%+v calls=%d err=%v stateErr=%v", result, state, calls, err, stateErr)
		}
	})
}

func TestStoreManagedUsageIncompleteAndFailedCensusStayDirty(t *testing.T) {
	for _, scenario := range []string{"physical-only-above-base", "incomplete", "over-effective", "collector-error", "overflow"} {
		t.Run(scenario, func(t *testing.T) {
			gate, c := newUsageCounterFixture(t)
			withUsageCounterGate(t, gate, func() {
				ctx := context.Background()
				if _, err := c.CompleteManagedLocked(ctx, 100, func(context.Context) (StoreBudgetResult, error) { return managedUsageMeasurement(1100, 1024), nil }); err != nil {
					t.Fatal(err)
				}
				if _, err := c.BeginLocked(ctx); err != nil {
					t.Fatal(err)
				}
				_, err := c.CompleteManagedLocked(ctx, 100, func(context.Context) (StoreBudgetResult, error) {
					switch scenario {
					case "physical-only-above-base":
						return StoreBudgetResult{PhysicalComplete: true, MeasuredBytes: 500}, nil
					case "incomplete":
						return StoreBudgetResult{MeasuredBytes: 40}, nil
					case "over-effective":
						return managedUsageMeasurement(1200, 1024), nil
					case "collector-error":
						return managedUsageMeasurement(40, 0), ErrStoreInventory
					default:
						return managedUsageMeasurement(math.MaxInt64, math.MaxInt64), nil
					}
				})
				state, readErr := c.readLocked()
				if err == nil || readErr != nil || !state.Dirty || state.ProtectedFloor != 1024 {
					t.Fatalf("unproved allowance made clean: %+v err=%v read=%v", state, err, readErr)
				}
			})
		})
	}
}

func TestStoreManagedUsageOverflowCanRecoverOnlyByFreshCensus(t *testing.T) {
	gate, c := newUsageCounterFixture(t)
	withUsageCounterGate(t, gate, func() {
		ctx := context.Background()
		if err := c.writeLocked(storeUsageState{Version: 1, Upper: math.MaxInt64, ProtectedFloor: math.MaxInt64, Epoch: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.CompleteManagedLocked(ctx, 100, nil); !errors.Is(err, ErrStoreInventory) {
			t.Fatal("unsafe scalar addition was accepted:", err)
		}
		state, err := c.readLocked()
		if err != nil || !state.Dirty {
			t.Fatalf("overflow did not fail closed: %+v %v", state, err)
		}
		result, err := c.CompleteManagedLocked(ctx, 100, func(context.Context) (StoreBudgetResult, error) {
			return StoreBudgetResult{PhysicalComplete: true, MeasuredBytes: 20}, nil
		})
		if err != nil || !result.Collected || result.ProtectedFloorBytes != 0 || result.UpperBytes != 20 {
			t.Fatalf("fresh census could not recover stale overflow: %+v %v", result, err)
		}
	})
}

func TestStoreManagedUsageStateFitsCommon256ByteProtocolAndStrictClearsGrace(t *testing.T) {
	max := storeUsageState{Version: 1, Upper: math.MaxInt64, ProtectedFloor: math.MaxInt64, Epoch: ^uint64(0)}
	data, err := json.Marshal(max)
	if err != nil || len(data) > storeUsageMaxJSON {
		t.Fatalf("scalar state exceeds common protocol: bytes=%d err=%v", len(data), err)
	}
	gate, c := newUsageCounterFixture(t)
	withUsageCounterGate(t, gate, func() {
		ctx := context.Background()
		if _, err := c.CompleteManagedLocked(ctx, 100, func(context.Context) (StoreBudgetResult, error) { return managedUsageMeasurement(1100, 1024), nil }); err != nil {
			t.Fatal(err)
		}
		calls := 0
		result, err := c.CompleteLocked(ctx, 100, func(context.Context) (int64, error) { calls++; return 40, nil })
		state, readErr := c.readLocked()
		if err != nil || readErr != nil || !result.Collected || calls != 1 || state.ProtectedFloor != 0 || state.Upper != 40 {
			t.Fatalf("strict API silently retained managed grace: %+v state=%+v calls=%d err=%v read=%v", result, state, calls, err, readErr)
		}
		for _, floor := range []int64{-1, 41} {
			if err := c.writeLocked(storeUsageState{Version: 1, Upper: 40, Epoch: 1, ProtectedFloor: floor}); err == nil {
				t.Fatal("invalid scalar floor accepted:", floor)
			}
		}
	})
}

func TestStoreManagedUsageFreshProcessCensusesExistingCleanGrace(t *testing.T) {
	gate, c := newUsageCounterFixture(t)
	withUsageCounterGate(t, gate, func() {
		if _, err := c.CompleteManagedLocked(context.Background(), 100, func(context.Context) (StoreBudgetResult, error) { return managedUsageMeasurement(1100, 1024), nil }); err != nil {
			t.Fatal(err)
		}
	})
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestStoreManagedUsageProcessHelper$")
	cmd.Env = append(os.Environ(), "SUPERCLI_MANAGED_USAGE_CHILD="+c.dataDir)
	childproc.HideWindow(cmd)
	output, err := cmd.CombinedOutput() // One completion wait, no progress polling.
	if err != nil || !strings.Contains(string(output), "synthetic managed census complete") {
		t.Fatalf("new process skipped proven-floor census: %v %s", err, output)
	}
}

func TestStoreManagedUsageProcessHelper(t *testing.T) {
	dir := os.Getenv("SUPERCLI_MANAGED_USAGE_CHILD")
	if dir == "" {
		return
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("absolute synthetic fixture required")
	}
	gate, err := NewStoreGate(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewStoreUsageCounter(gate)
	if err != nil {
		t.Fatal(err)
	}
	withUsageCounterGate(t, gate, func() {
		calls := 0
		result, err := c.CompleteManagedLocked(context.Background(), 100, func(context.Context) (StoreBudgetResult, error) {
			calls++
			return managedUsageMeasurement(1040, 1000), nil
		})
		if err != nil || calls != 1 || !result.Collected || result.ProtectedFloorBytes != 1000 {
			t.Fatalf("fresh process trusted old clean floor: %+v calls=%d err=%v", result, calls, err)
		}
	})
	fmt.Println("synthetic managed census complete")
}
