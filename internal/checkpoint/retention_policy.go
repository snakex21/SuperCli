package checkpoint

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SessionRetention extends the journaled collector, not Git gc. Existing unsafe,
// active, index, reflog, packed and unknown roots remain mandatory.
type SessionRetention struct {
	KeepPerSession int
	MaxAge         time.Duration
}

// PruneSessionsLocked requires StoreGate and a complete session ID census.
// Recent orphan records get the same grace period as live history. Running
// sessions (and stores with pending recovery roots) are never expired.
func PruneSessionsLocked(ctx context.Context, dataDir string, sessions, running map[string]bool, policy SessionRetention, now time.Time) (int, error) {
	if sessions == nil || policy.KeepPerSession < 0 || policy.MaxAge <= 0 {
		return 0, ErrStoreInventory
	}
	if err := resumeRetentionJournalLocked(ctx, dataDir); err != nil {
		return 0, err
	}
	if _, err := retireRetentionJournalLocked(dataDir); err != nil {
		return 0, err
	}
	audit, err := AuditStoreRetentionLocked(ctx, dataDir)
	if err != nil {
		return 0, err
	}
	plan := sessionRetentionPlan(audit, sessions, running, policy, now)
	if len(plan.Expired) != 0 || len(audit.Inventory.Unreferenced) != 0 {
		// Expiry frontiers can grow even while objects shrink. Invalidate the
		// shared upper-bound ledger before the first write; the next turn performs
		// a fresh census instead of trusting stale pre-maintenance accounting.
		gate, err := NewStoreGate(dataDir)
		if err != nil {
			return 0, err
		}
		counter, err := NewStoreUsageCounter(gate)
		if err != nil {
			return 0, err
		}
		if _, err := counter.BeginLocked(ctx); err != nil {
			return 0, err
		}
	}
	if len(plan.Expired) != 0 {
		journal, err := makeRetentionJournal(audit, plan)
		if err != nil {
			return 0, err
		}
		if err := writeRetentionJournal(dataDir, journal); err != nil {
			return 0, err
		}
		if err := resumeRetentionJournalLocked(ctx, dataDir); err != nil {
			return 0, err
		}
		for _, key := range plan.Expired {
			log.Printf("housekeeping: expired checkpoint store=%s record=%s", key.Store, key.ID)
		}
		if _, err := retireRetentionJournalLocked(dataDir); err != nil {
			return len(plan.Expired), err
		}
		audit, err = AuditStoreRetentionLocked(ctx, dataDir)
		if err != nil {
			return len(plan.Expired), err
		}
	}
	_, err = deleteRetentionLooseLocked(ctx, audit, audit.Inventory.Unreferenced)
	for _, root := range audit.Roots {
		if root.Bytes > 0 {
			log.Printf("housekeeping: checkpoint protected store=%s root=%s bytes=%d", root.Store, root.Kind, root.Bytes)
		}
	}
	for _, p := range audit.Protected {
		log.Printf("housekeeping: checkpoint retained %+v", p)
	}
	return len(plan.Expired), err
}

func retentionStoreHasLiveLease(a *StoreRetentionAudit, store string) bool {
	for path := range a.files {
		if !strings.HasPrefix(path, store+"/leases/") {
			continue
		}
		full := filepath.Join(a.dataDir, filepath.FromSlash(path))
		if err := retentionSafePath(a.dataDir, full); err != nil {
			return true
		}
		unlock, err := checkpointStoreLock(full)
		if errors.Is(err, ErrStoreBusy) || err != nil {
			return true
		}
		if err := unlock(); err != nil {
			return true
		}
	}
	return false
}

func sessionRetentionPlan(a *StoreRetentionAudit, sessions, running map[string]bool, policy SessionRetention, now time.Time) RetentionPlan {
	protected := map[RetentionKey]bool{}
	for _, r := range a.Inventory.Records {
		protected[r.Key] = r.Protected
	}
	bySession := map[string][]RetentionRecord{}
	for store, repo := range a.repos {
		// A native lease spans the accepted turn, including tool-call gaps.
		// Probe under StoreGate: abandoned unlocked receipts are not living runs.
		// Their Git recovery roots still remain independently protected by audit.
		leased := retentionStoreHasLiveLease(a, store)
		for i, r := range repo.records {
			key := RetentionKey{store, r.ID}
			if leased || running[r.SessionID] || protected[key] || r.CreatedAt.IsZero() || !r.CreatedAt.Before(now.Add(-policy.MaxAge)) {
				protected[key] = true
			}
			if sessions[r.SessionID] {
				bySession[r.SessionID] = append(bySession[r.SessionID], RetentionRecord{Key: key, CreatedAt: r.CreatedAt, Order: i})
			}
		}
	}
	for _, records := range bySession {
		sort.Slice(records, func(i, j int) bool {
			if !records[i].CreatedAt.Equal(records[j].CreatedAt) {
				return records[i].CreatedAt.After(records[j].CreatedAt)
			}
			if records[i].Order != records[j].Order {
				return records[i].Order > records[j].Order
			}
			return records[i].Key.ID > records[j].Key.ID
		})
		for i := 0; i < len(records) && i < policy.KeepPerSession; i++ {
			protected[records[i].Key] = true
		}
	}
	plan := RetentionPlan{Evidence: a.Inventory.Evidence}
	for _, r := range a.Inventory.Records {
		if protected[r.Key] {
			plan.Kept = append(plan.Kept, r.Key)
		} else {
			plan.Expired = append(plan.Expired, r.Key)
		}
	}
	return plan
}
