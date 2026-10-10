package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	retentionJournalName = "checkpoints/.retention-journal.json"
	retentionMaxJournal  = 16 << 20
	// Cleanup adds at most this bounded metadata/frontier/journal staging over
	// its starting physical usage. Pre-existing garbage and active snapshots
	// can already exceed the final retained limit until collection completes.
	StoreRetentionMaxTransientBytes int64 = 2*retentionMaxMetadata + 2*retentionMaxJournal
)

type StoreBudgetResult struct {
	PhysicalComplete   bool
	FullCensusComplete bool
	Files              int
	MeasuredBytes      int64
	ProtectedBytes     int64
	KnownReclaimBytes  int64
	UnreferencedBytes  int64
	ReclaimedBytes     int64
	Expired            []RetentionKey
	Protected          []RetentionProtection
	Roots              []RetentionRoot
}

type retentionJournalRepo struct {
	Store             string            `json:"store"`
	OriginalSHA       string            `json:"original_sha256"`
	NextSHA           string            `json:"next_sha256"`
	Next              json.RawMessage   `json:"next_metadata"`
	Refs              map[string]string `json:"owned_refs"`
	Expired           []RetentionKey    `json:"expired"`
	ExpiryOriginalSHA string            `json:"expiry_original_sha256,omitempty"`
	ExpiryNextSHA     string            `json:"expiry_next_sha256"`
	ExpiryNext        json.RawMessage   `json:"next_expiry"`
}

type retentionJournal struct {
	Version int                    `json:"version"`
	Phase   string                 `json:"phase"`
	Repos   []retentionJournalRepo `json:"repos"`
}

// EnforceStoreBudgetLocked runs only at first quota activation/invalidation or
// pressure. Caller already owns the portable StoreGate, and all writers must
// cooperate with it. It never acquires a second Gate or starts background work.
// The limit applies to final compressed files in checkpoints + badcheckpoints,
// including unknown/pending/packed/index/reflog data. Active snapshots and
// cleanup staging remain protected during collection. reserve optionally leaves
// future headroom; the normal post-turn policy passes zero. This is a final
// retained cap, not strict pre-write admission: live snapshots/temporary writes
// have their separate capture limits, and an unattainable protected floor is a
// visible post-turn quota error, not permission to delete unknown roots.
// Unknown/latest/index/reflog roots are never deleted by name, age or PID.
func EnforceStoreBudgetLocked(ctx context.Context, dataDir string, limit, reserve int64) (result StoreBudgetResult, err error) {
	if limit <= 0 || reserve < 0 {
		return result, ErrStoreInventory
	}
	held := reserve
	if held >= limit {
		return result, ErrStoreBudget
	}
	if err := resumeRetentionJournalLocked(ctx, dataDir); err != nil {
		return result, err
	}
	// A validated sweep receipt cannot replay metadata anymore. Remove it
	// before census, so cleanup's own temporary payload cannot prevent reaching
	// the protected floor. A crash during later loose deletion needs only a new
	// graph census; the durable frontier and kept roots are already committed.
	recoveredJournalBytes, err := retireRetentionJournalLocked(dataDir)
	if err != nil {
		return result, err
	}
	result.ReclaimedBytes = recoveredJournalBytes
	physical, err := retentionMeasureFilesLocked(ctx, dataDir)
	if err != nil {
		return result, err
	}
	result.PhysicalComplete, result.MeasuredBytes, result.Files = true, physical.measuredBytes, len(physical.files)
	if physical.measuredBytes <= limit-held {
		return result, nil // No pressure: no Git commands or metadata reads.
	}
	audit, err := physical.classifyStoreRetentionLocked(ctx)
	if err != nil {
		return result, err
	}
	result.FullCensusComplete = true
	plan, journal, planErr := planRetentionWithExpiry(audit, limit-held)
	result.MeasuredBytes, result.ProtectedBytes = plan.BeforeBytes, plan.ProtectedBytes
	result.Protected = audit.Protected
	result.Roots = audit.Roots
	result.KnownReclaimBytes = plan.BeforeBytes - plan.ProtectedBytes
	for _, u := range audit.Inventory.Unreferenced {
		result.UnreferencedBytes, err = retentionAdd(result.UnreferencedBytes, u.Bytes)
		if err != nil {
			return result, err
		}
	}
	if planErr != nil {
		// A complete census may prove redundant loose files even when the
		// mandatory floor cannot fit. Reclaim those without expiring history;
		// the quota error still truthfully reports that the final cap is unmet.
		if errors.Is(planErr, ErrStoreBudget) && len(audit.Inventory.Unreferenced) != 0 {
			deleted, deleteErr := deleteRetentionLooseLocked(ctx, audit, audit.Inventory.Unreferenced)
			if deleted > plan.BeforeBytes {
				return result, ErrStoreInventory
			}
			result.MeasuredBytes -= deleted
			result.ReclaimedBytes, err = retentionAdd(recoveredJournalBytes, deleted)
			if err != nil {
				return result, err
			}
			return result, errors.Join(planErr, deleteErr)
		}
		return result, planErr // No pointless history expiry above protected floor.
	}
	if len(plan.Expired) == 0 && len(plan.Reclaim) == 0 {
		return result, nil // One census; no pressure means no graph work again.
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
	}
	// Re-read all roots after metadata/ref changes. A surviving latest/index or
	// unknown ref can still protect an expired snapshot; never infer reclaim
	// from the metadata list alone or run broad git prune/gc.
	fresh := audit
	if len(plan.Expired) != 0 {
		fresh, err = AuditStoreRetentionLocked(ctx, dataDir)
		if err != nil {
			return result, err
		}
	}
	deleted := int64(0)
	if plan.BeforeBytes > limit-held {
		deleted, err = deleteRetentionLooseLocked(ctx, fresh, fresh.Inventory.Unreferenced)
		if err != nil {
			return result, err
		}
	}
	// Fresh census already measured the changed metadata/refs. The held Gate
	// permits exact subtraction of only confirmed deletions; no optimistic
	// estimates and no need to walk all kept graphs for a third time.
	freshPlan, err := PlanStoreRetention(fresh.Inventory, limit-held)
	if err != nil && !errors.Is(err, ErrStoreBudget) {
		return result, err
	}
	if freshPlan.BeforeBytes < deleted {
		return result, ErrStoreInventory
	}
	result.MeasuredBytes = freshPlan.BeforeBytes - deleted
	result.ProtectedBytes = freshPlan.ProtectedBytes
	result.Protected = fresh.Protected
	result.Roots = fresh.Roots
	if plan.BeforeBytes > result.MeasuredBytes {
		result.ReclaimedBytes, err = retentionAdd(recoveredJournalBytes, plan.BeforeBytes-result.MeasuredBytes)
		if err != nil {
			return result, err
		}
	}
	if err := CheckStoreBudgetAdmission(limit, result.MeasuredBytes, held); err != nil {
		return result, err
	}
	return result, nil
}

// Account for the exact durable frontier/metadata growth before approving an
// expiry decision. Usually removing a Record saves more JSON than its frontier
// adds. Monotone headroom avoids oscillation if a subsequent plan saves JSON.
// Planning reads no additional objects; a bounded pathological metadata case
// fails before any write rather than silently exceeding the final limit.
func planRetentionWithExpiry(a *StoreRetentionAudit, limit int64) (RetentionPlan, retentionJournal, error) {
	var plan RetentionPlan
	var journal retentionJournal
	var extra int64
	for attempt := 0; attempt < 32; attempt++ {
		var err error
		plan, err = PlanStoreRetention(a.Inventory, limit-extra)
		if err != nil || len(plan.Expired) == 0 {
			return plan, journal, err
		}
		journal, err = makeRetentionJournal(a, plan)
		if err != nil {
			return plan, journal, err
		}
		growth := int64(0)
		for _, r := range journal.Repos {
			meta := a.files[r.Store+"/turns.json"]
			if meta == nil {
				return plan, journal, ErrStoreInventory
			}
			delta := int64(len(r.Next)+len(r.ExpiryNext)) - meta.Size()
			if old := a.files[r.Store+"/"+retentionExpiryName]; old != nil {
				delta -= old.Size()
			}
			if delta > 0 {
				growth, err = retentionAdd(growth, delta)
				if err != nil {
					return plan, journal, err
				}
			}
		}
		if growth <= extra {
			return plan, journal, nil
		}
		if growth >= limit {
			return plan, journal, ErrStoreBudget
		}
		extra = growth
	}
	return plan, journal, ErrStoreInventory
}

func retireRetentionJournalLocked(dataDir string) (int64, error) {
	path := filepath.Join(dataDir, filepath.FromSlash(retentionJournalName))
	if err := retentionSafePath(dataDir, path); err != nil {
		return 0, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	proof, err := retentionProveFile(path, info)
	if err != nil {
		return 0, err
	}
	if err := retentionRemoveProven(path, proof); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func makeRetentionJournal(a *StoreRetentionAudit, plan RetentionPlan) (retentionJournal, error) {
	j := retentionJournal{Version: 1, Phase: "prepared"}
	expire := map[RetentionKey]bool{}
	for _, key := range plan.Expired {
		expire[key] = true
	}
	names := make([]string, 0, len(a.repos))
	for store := range a.repos {
		names = append(names, store)
	}
	sort.Strings(names)
	for _, store := range names {
		r := a.repos[store]
		entry := retentionJournalRepo{Store: store, OriginalSHA: r.metaHash, Refs: map[string]string{}}
		kept := make([]json.RawMessage, 0, len(r.records))
		expired := make([]Record, 0)
		for i, record := range r.records {
			key := RetentionKey{store, record.ID}
			if !expire[key] {
				kept = append(kept, r.raw[i])
				continue
			}
			entry.Expired = append(entry.Expired, key)
			expired = append(expired, record)
			for name, oid := range r.owned[key] {
				entry.Refs[name] = oid
			}
		}
		if len(entry.Expired) == 0 {
			continue
		}
		data, err := json.Marshal(kept)
		if err != nil {
			return j, err
		}
		entry.Next, entry.NextSHA = data, retentionSHA(data)
		frontier, err := retentionAdvanceExpiry(r.expiryRaw, expired)
		if err != nil {
			return j, err
		}
		entry.ExpiryOriginalSHA = r.expiryHash
		entry.ExpiryNext, entry.ExpiryNextSHA = frontier, retentionSHA(frontier)
		j.Repos = append(j.Repos, entry)
	}
	return j, nil
}

func writeRetentionJournal(dataDir string, j retentionJournal) error {
	data, err := json.Marshal(j)
	if err != nil || len(data) > retentionMaxJournal {
		return ErrStoreInventory
	}
	return retentionAtomicWrite(dataDir, filepath.Join(dataDir, filepath.FromSlash(retentionJournalName)), data)
}

// Resume is an exact journal roll-forward, never a new retention decision.
// Metadata is switched before deleting owned refs. A crash can retain extra
// roots/bytes, but cannot leave kept records pointing at swept objects.
func resumeRetentionJournalLocked(ctx context.Context, dataDir string) error {
	journalPath := filepath.Join(dataDir, filepath.FromSlash(retentionJournalName))
	if err := retentionSafePath(dataDir, journalPath); err != nil {
		return err
	}
	if _, err := os.Lstat(journalPath); os.IsNotExist(err) {
		return nil
	}
	data, err := retentionRead(journalPath, retentionMaxJournal)
	if err != nil {
		return err
	}
	var j retentionJournal
	if json.Unmarshal(data, &j) != nil || j.Version != 1 || (j.Phase != "prepared" && j.Phase != "metadata" && j.Phase != "sweep") || len(j.Repos) == 0 {
		return ErrStoreInventory
	}
	stores := map[string]bool{}
	for _, r := range j.Repos {
		if !validRetentionStore(r.Store) || stores[r.Store] || !validRetentionOID(r.OriginalSHA, 64) || !validRetentionOID(r.NextSHA, 64) || retentionSHA(r.Next) != r.NextSHA || len(r.Expired) == 0 || (r.ExpiryOriginalSHA != "" && !validRetentionOID(r.ExpiryOriginalSHA, 64)) || !validRetentionOID(r.ExpiryNextSHA, 64) || retentionSHA(r.ExpiryNext) != r.ExpiryNextSHA {
			return ErrStoreInventory
		}
		if _, err := retentionDecodeExpiry(r.ExpiryNext); err != nil {
			return err
		}
		stores[r.Store] = true
		allowed := map[string]bool{}
		for _, key := range r.Expired {
			if key.Store != r.Store || key.ID == "" {
				return ErrStoreInventory
			}
			allowed[recordRefRoot(key.ID)+"/before"] = true
			allowed[recordRefRoot(key.ID)+"/after"] = true
		}
		for name, oid := range r.Refs {
			if !allowed[name] || !validRetentionOID(oid, 40) {
				return ErrStoreInventory
			}
		}
		if j.Phase == "sweep" {
			continue // Metadata/ref transaction already completed durably.
		}
		_, expiryHash, err := retentionReadExpiry(dataDir, filepath.Join(dataDir, filepath.FromSlash(r.Store), retentionExpiryName))
		if err != nil || (expiryHash != r.ExpiryOriginalSHA && expiryHash != r.ExpiryNextSHA) || (j.Phase != "prepared" && expiryHash != r.ExpiryNextSHA) {
			return ErrStoreInventory
		}
		meta := filepath.Join(dataDir, filepath.FromSlash(r.Store), "turns.json")
		if err := retentionSafePath(dataDir, meta); err != nil {
			return err
		}
		current, err := retentionRead(meta, retentionMaxMetadata)
		if err != nil {
			return err
		}
		hash := retentionSHA(current)
		if hash != r.OriginalSHA && hash != r.NextSHA {
			return fmt.Errorf("%w: retention metadata changed in %s", ErrStoreInventory, r.Store)
		}
		if j.Phase != "prepared" && hash != r.NextSHA {
			return ErrStoreInventory
		}
	}
	if j.Phase == "prepared" {
		for _, r := range j.Repos {
			if err := ctx.Err(); err != nil {
				return err
			}
			meta := filepath.Join(dataDir, filepath.FromSlash(r.Store), "turns.json")
			frontier := filepath.Join(filepath.Dir(meta), retentionExpiryName)
			_, expiryHash, err := retentionReadExpiry(dataDir, frontier)
			if err != nil {
				return err
			}
			if expiryHash == r.ExpiryOriginalSHA {
				// Persist the refusal boundary before removing even one record.
				if err := retentionAtomicWrite(dataDir, frontier, r.ExpiryNext); err != nil {
					return err
				}
			}
			current, err := retentionRead(meta, retentionMaxMetadata)
			if err != nil {
				return err
			}
			if retentionSHA(current) == r.OriginalSHA {
				if err := retentionAtomicWrite(dataDir, meta, r.Next); err != nil {
					return err
				}
			}
		}
		j.Phase = "metadata"
		if err := writeRetentionJournal(dataDir, j); err != nil {
			return err
		}
	}
	if j.Phase == "metadata" {
		for _, r := range j.Repos {
			repo := filepath.Join(dataDir, filepath.FromSlash(r.Store), "objects.git")
			if err := retentionSafePath(dataDir, repo); err != nil {
				return err
			}
			a := &StoreRetentionAudit{}
			actual := map[string]string{}
			if err := a.gitTokens(ctx, repo, nil, false, func(line string) error {
				parts := strings.Split(line, "\t")
				if len(parts) != 3 || parts[2] != "" {
					return ErrStoreInventory
				}
				actual[parts[0]] = parts[1]
				return nil
			}, "for-each-ref", "--format=%(refname)%09%(objectname)%09%(symref)"); err != nil {
				return err
			}
			names := make([]string, 0, len(r.Refs))
			for name, oid := range r.Refs {
				if actual[name] == "" {
					continue // An earlier journal attempt already removed it.
				}
				if actual[name] != oid {
					return ErrStoreInventory
				}
				names = append(names, name)
			}
			sort.Strings(names)
			if len(names) != 0 {
				var input strings.Builder
				input.WriteString("start\n")
				for _, name := range names {
					fmt.Fprintf(&input, "delete %s %s\n", name, r.Refs[name])
				}
				input.WriteString("prepare\ncommit\n")
				cmd := retentionGitCommand(ctx, repo, "-c", "core.fsync=reference", "update-ref", "--no-deref", "--stdin")
				cmd.Stdin = strings.NewReader(input.String())
				if err := cmd.Run(); err != nil {
					return errors.Join(ErrStoreInventory, ctx.Err())
				}
			}
		}
		j.Phase = "sweep"
		if err := writeRetentionJournal(dataDir, j); err != nil {
			return err
		}
	}
	return nil
}

func deleteRetentionLooseLocked(ctx context.Context, a *StoreRetentionAudit, units []RetentionUnit) (int64, error) {
	// Validate every candidate before deleting the first. Under the held gate,
	// no cooperating writer can change roots or replace these measured files.
	allowed := map[string]bool{}
	for _, u := range a.Inventory.Unreferenced {
		allowed[u.Path] = true
	}
	for _, u := range units {
		if !allowed[u.Path] || a.files[u.Path] == nil {
			return 0, ErrStoreInventory
		}
		full := filepath.Join(a.dataDir, filepath.FromSlash(u.Path))
		if err := retentionSafePath(a.dataDir, full); err != nil {
			return 0, err
		}
		proof := a.proofs[u.Path]
		if proof == nil || proof.info.Size() != u.Bytes {
			return 0, ErrStoreInventory
		}
		if err := retentionCheckProof(full, proof); err != nil {
			return 0, err
		}
	}
	var deleted int64
	for _, u := range units {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		full := filepath.Join(a.dataDir, filepath.FromSlash(u.Path))
		if err := retentionSafePath(a.dataDir, full); err != nil {
			return deleted, err
		}
		if err := retentionRemoveProven(full, a.proofs[u.Path]); err != nil {
			return deleted, err
		}
		var err error
		deleted, err = retentionAdd(deleted, u.Bytes)
		if err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}
