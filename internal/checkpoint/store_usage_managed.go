package checkpoint

import (
	"context"
	"errors"
	"fmt"
)

// StoreManagedEffectiveLimit is the explicit policy exception: when a complete
// census proves a protected floor larger than the history target, the target is
// additional headroom above that floor. This is not a strict whole-store cap.
// Floor <= base grants no exception. All arithmetic fails closed on overflow.
func StoreManagedEffectiveLimit(base, provenFloor int64) (effective, allowance int64, err error) {
	if base <= 0 || provenFloor < 0 {
		return 0, 0, ErrStoreInventory
	}
	if provenFloor <= base {
		return base, 0, nil
	}
	effective, err = retentionAdd(base, provenFloor)
	if err != nil {
		return 0, 0, err
	}
	return effective, provenFloor, nil
}

type StoreManagedUsageCompletion struct {
	StoreUsageCompletion
	BaseLimitBytes      int64
	EffectiveLimitBytes int64
	ProtectedFloorBytes int64
}

// CompleteManagedLocked preserves one scalar last-proved floor between complete
// censuses. Known bounded writes only increase Upper. First process completion,
// dirty/unknown state and pressure still force census; none start background
// work. Clear/Forget/unknown layout/root changes must use the existing durable
// dirty protocol, so a reduced floor is recomputed before new grace is granted.
// The last-census allowance can outlive a root until invalidation/pressure; it is
// not a claim that every currently retained byte is protected or undo history.
// Callers expose the allowance/effective limit, not a strict 1 GiB total promise.
//
// collect owns this same gate and returns successful, physically complete output
// from EnforceManagedStoreBudgetLocked. Above base, FullCensusComplete is required
// before publishing even a zero floor. A physical-only check against a cached
// larger limit must never wash out the old floor and pretend base was enforced.
func (c *StoreUsageCounter) CompleteManagedLocked(ctx context.Context, base int64, collect func(context.Context) (StoreBudgetResult, error)) (result StoreManagedUsageCompletion, err error) {
	result.BaseLimitBytes = base
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if base <= 0 {
		return result, ErrStoreInventory
	}
	state, readErr := c.readLocked()
	if readErr != nil && !errors.Is(readErr, ErrStoreUsageCensus) {
		return result, readErr
	}
	result.EffectiveLimitBytes = base
	var cachedLimitErr error
	if readErr == nil {
		result.UpperBytes = state.Upper
		effective, allowance, limitErr := StoreManagedEffectiveLimit(base, state.ProtectedFloor)
		cachedLimitErr = limitErr
		if limitErr == nil {
			result.EffectiveLimitBytes, result.ProtectedFloorBytes = effective, allowance
		}
		if limitErr == nil && !state.Dirty && state.Upper <= effective && storeUsageTrust(c.key, false) {
			return result, nil
		}
	}
	if collect == nil {
		if cachedLimitErr != nil {
			// Unsafe cached arithmetic must not stay clean; a later real census
			// can still recover a stale allowance instead of poisoning the store.
			_, beginErr := c.BeginLocked(ctx)
			return result, errors.Join(cachedLimitErr, beginErr)
		}
		return result, ErrStoreUsageCensus
	}
	w, err := c.BeginLocked(ctx) // Collection changes metadata/refs too.
	if err != nil {
		return result, err
	}
	measured, err := collect(ctx)
	result.Collected = true
	if err != nil {
		return result, err // Incomplete/failed collection deliberately stays dirty.
	}
	if !measured.PhysicalComplete || measured.MeasuredBytes < 0 || measured.ProtectedBytes < 0 || measured.ProtectedBytes > measured.MeasuredBytes || (measured.MeasuredBytes > base && !measured.FullCensusComplete) {
		return result, ErrStoreInventory
	}
	effective, allowance := base, int64(0)
	if measured.FullCensusComplete {
		effective, allowance, err = StoreManagedEffectiveLimit(base, measured.ProtectedBytes)
		if err != nil {
			return result, err
		}
	}
	result.EffectiveLimitBytes, result.ProtectedFloorBytes = effective, allowance
	if measured.MeasuredBytes > effective {
		return result, fmt.Errorf("%w: measured %d bytes exceed managed limit %d bytes", ErrStoreBudget, measured.MeasuredBytes, effective)
	}
	if w.state.Epoch == ^uint64(0) {
		return result, ErrStoreInventory
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	fresh, err := c.readLocked()
	if err != nil || fresh != w.state {
		return result, errors.Join(ErrStoreUsageCensus, err)
	}
	fresh.Upper, fresh.ProtectedFloor, fresh.Dirty, fresh.Epoch = measured.MeasuredBytes, allowance, false, fresh.Epoch+1
	if err := c.writeLocked(fresh); err != nil {
		return result, err
	}
	w.done = true
	storeUsageTrust(c.key, true)
	result.UpperBytes = measured.MeasuredBytes
	return result, nil
}
