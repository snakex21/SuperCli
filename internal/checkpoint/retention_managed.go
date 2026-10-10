package checkpoint

import (
	"context"
	"errors"
)

// EnforceManagedStoreBudgetLocked targets base bytes of eligible history above
// a larger proven protected floor. It preserves the strict Enforce API and all
// its tests. The exception is explicit: final measured <= base+floor only when
// a complete classification proves floor>base; otherwise measured <= base.
// All writers cooperate with the caller's held gate. No cached floor is accepted
// here, no graph/history cache survives return, and unknown artifacts stay fixed.
func EnforceManagedStoreBudgetLocked(ctx context.Context, dataDir string, base int64) (result StoreBudgetResult, err error) {
	if base <= 0 {
		return result, ErrStoreInventory
	}
	if err := resumeRetentionJournalLocked(ctx, dataDir); err != nil {
		return result, err
	}
	recoveredJournal, err := retireRetentionJournalLocked(dataDir)
	if err != nil {
		return result, err
	}
	result.ReclaimedBytes = recoveredJournal
	audit, err := retentionMeasureFilesLocked(ctx, dataDir)
	if err != nil {
		return result, err
	}
	result.PhysicalComplete, result.MeasuredBytes, result.Files = true, audit.measuredBytes, len(audit.files)
	if audit.measuredBytes <= base {
		return result, nil // Only base, never a cached larger allowance.
	}
	audit, err = audit.classifyStoreRetentionLocked(ctx)
	if err != nil {
		return result, err
	}
	result.FullCensusComplete = true
	startingBytes := audit.measuredBytes
	// Normally one pass. If expiry shrinks a just-over-base floor below base,
	// the fresh post-expiry audit is replanned against base without another
	// pre-expiry Git traversal. The hard bound refuses pathological churn.
	for pass := 0; pass < 32; pass++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		floorPlan, planErr := PlanStoreRetention(audit.Inventory, base)
		if planErr != nil && !errors.Is(planErr, ErrStoreBudget) {
			return result, planErr
		}
		result.MeasuredBytes, result.ProtectedBytes = floorPlan.BeforeBytes, floorPlan.ProtectedBytes
		result.Protected, result.Roots, result.Files = audit.Protected, audit.Roots, len(audit.files)
		if pass == 0 {
			result.KnownReclaimBytes = floorPlan.BeforeBytes - floorPlan.ProtectedBytes
			for _, unit := range audit.Inventory.Unreferenced {
				result.UnreferencedBytes, err = retentionAdd(result.UnreferencedBytes, unit.Bytes)
				if err != nil {
					return result, err
				}
			}
		}
		effective, allowance, err := StoreManagedEffectiveLimit(base, floorPlan.ProtectedBytes)
		if err != nil {
			return result, err
		}
		var plan RetentionPlan
		var journal retentionJournal
		if allowance != 0 {
			// Added frontier/metadata is fixed too, so it increases both final
			// physical usage and the freshly verified floor. Eligible bytes must
			// fit base; no second initial root census or guessed reserve is used.
			plan, planErr = PlanStoreRetention(audit.Inventory, effective)
			if planErr == nil && len(plan.Expired) != 0 {
				journal, planErr = makeRetentionJournal(audit, plan)
			}
		} else {
			plan, journal, planErr = planRetentionWithExpiry(audit, effective)
		}
		if planErr != nil {
			return result, planErr
		}
		if len(plan.Expired) == 0 && len(plan.Reclaim) == 0 {
			return result, CheckStoreBudgetAdmission(effective, result.MeasuredBytes, 0)
		}
		if len(plan.Expired) != 0 {
			if err := writeRetentionJournal(dataDir, journal); err != nil {
				return result, err
			}
			if err := resumeRetentionJournalLocked(ctx, dataDir); err != nil {
				return result, err
			}
			result.Expired = append(result.Expired, plan.Expired...)
			if _, err := retireRetentionJournalLocked(dataDir); err != nil {
				return result, err
			}
			// Re-prove every kept/unknown/active/index/reflog root before sweep.
			audit, err = AuditStoreRetentionLocked(ctx, dataDir)
			if err != nil {
				return result, err
			}
		}
		units := audit.Inventory.Unreferenced
		deleted, err := deleteRetentionLooseLocked(ctx, audit, units)
		if err != nil {
			return result, err // No optimistic clean baseline after partial cleanup.
		}
		// Only confirmed deletions update this already-fresh local audit. It is
		// still under the same gate, and is discarded when this call ends.
		for _, unit := range units {
			delete(audit.files, unit.Path)
			delete(audit.proofs, unit.Path)
		}
		audit.Inventory.Unreferenced = nil
		if deleted > audit.measuredBytes {
			return result, ErrStoreInventory
		}
		audit.measuredBytes -= deleted
		audit.Inventory.Evidence = audit.evidence()
		freshPlan, freshErr := PlanStoreRetention(audit.Inventory, base)
		if freshErr != nil && !errors.Is(freshErr, ErrStoreBudget) {
			return result, freshErr
		}
		result.MeasuredBytes, result.ProtectedBytes = freshPlan.BeforeBytes, freshPlan.ProtectedBytes
		result.Protected, result.Roots, result.Files = audit.Protected, audit.Roots, len(audit.files)
		if startingBytes > result.MeasuredBytes {
			result.ReclaimedBytes, err = retentionAdd(recoveredJournal, startingBytes-result.MeasuredBytes)
			if err != nil {
				return result, err
			}
		}
		finalLimit, _, err := StoreManagedEffectiveLimit(base, result.ProtectedBytes)
		if err != nil {
			return result, err
		}
		if result.MeasuredBytes <= finalLimit {
			return result, nil
		}
		// Only threshold-crossing floor shrink requires another expiry pass.
		// Reuse this fresh root proof, never recensus before replanning it.
	}
	return result, ErrStoreInventory
}
