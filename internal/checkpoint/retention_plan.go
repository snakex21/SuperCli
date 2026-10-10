package checkpoint

import (
	"errors"
	"fmt"
	"math"
	"path"
	"runtime"
	"sort"
	"strings"
	"time"
)

const DefaultStoreBudgetBytes int64 = 1 << 30

var (
	ErrStoreBudget    = errors.New("checkpoint store budget exceeded")
	ErrStoreInventory = errors.New("checkpoint store inventory is incomplete or unsafe")
)

// RetentionUnit is one physical file, not an object ID or an uncompressed blob
// size. Path is relative to the portable data directory. The same object in two
// repositories consumes space twice; a shared object in one repository once.
type RetentionUnit struct {
	Path  string
	Bytes int64
}

type RetentionKey struct {
	Store string
	ID    string
}

type RetentionRecord struct {
	Key       RetentionKey
	CreatedAt time.Time
	Order     int
	Units     []RetentionUnit
	Protected bool
}

// Complete means every file in both checkpoints and badcheckpoints was
// classified. Fixed includes every unknown, active, incomplete, packed,
// alternate, reflog/index-rooted and other unclaimed file. Omitting a physical
// file or treating an estimate as a measured size makes an inventory invalid.
// Evidence binds an apply to a fresh census and root/metadata digests.
type RetentionInventory struct {
	Complete     bool
	Evidence     string
	Fixed        []RetentionUnit
	Unreferenced []RetentionUnit
	Records      []RetentionRecord
}

type RetentionPlan struct {
	Evidence       string
	LimitBytes     int64
	BeforeBytes    int64
	AfterBytes     int64
	ProtectedBytes int64
	Expired        []RetentionKey
	Kept           []RetentionKey
	Reclaim        []RetentionUnit
}

func retentionPathKey(p string) (string, error) {
	if p == "" || p == "." || strings.ContainsAny(p, "\\\x00:") || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return "", ErrStoreInventory
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == "." || part == "" {
			return "", ErrStoreInventory
		}
	}
	if runtime.GOOS == "windows" {
		return strings.ToLower(p), nil
	}
	return p, nil
}

func retentionAdd(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, ErrStoreInventory
	}
	return a + b, nil
}

// CheckStoreBudgetAdmission checks both retained and transient bytes. Reserve
// must bound the complete next transaction, including before/after captures,
// compression overhead, temporary index/metadata, ref updates and recovery
// files. Call before creating the first temporary file, never after copying it.
func CheckStoreBudgetAdmission(limit, measured, reserve int64) error {
	if limit <= 0 {
		return ErrStoreInventory
	}
	need, err := retentionAdd(measured, reserve)
	if err != nil {
		return err
	}
	if need > limit {
		return fmt.Errorf("%w: store %d bytes plus transaction reserve %d bytes exceeds %d bytes", ErrStoreBudget, measured, reserve, limit)
	}
	return nil
}

// PlanStoreRetention expires globally oldest records only under pressure.
// It performs no I/O and makes no deletion claims: AfterBytes is attainable
// only after fresh-root validation and actual reclamation of unreferenced
// files. If mandatory roots alone exceed the limit, no history is expired.
func PlanStoreRetention(in RetentionInventory, limit int64) (RetentionPlan, error) {
	plan := RetentionPlan{Evidence: in.Evidence, LimitBytes: limit}
	if !in.Complete || in.Evidence == "" || limit < 0 {
		return plan, ErrStoreInventory
	}
	type cost struct {
		bytes int64
		refs  int
		fixed bool
	}
	units := make(map[string]*cost)
	add := func(u RetentionUnit, fixed bool) (string, error) {
		key, err := retentionPathKey(u.Path)
		if err != nil || u.Bytes < 0 {
			return "", ErrStoreInventory
		}
		c := units[key]
		if c == nil {
			c = &cost{bytes: u.Bytes}
			units[key] = c
			plan.BeforeBytes, err = retentionAdd(plan.BeforeBytes, u.Bytes)
			if err != nil {
				return "", err
			}
		} else if c.bytes != u.Bytes {
			return "", ErrStoreInventory
		}
		c.fixed = c.fixed || fixed
		return key, nil
	}
	for _, u := range in.Fixed {
		if _, err := add(u, true); err != nil {
			return plan, err
		}
	}
	keys := make(map[RetentionKey]bool)
	recordUnits := make([][]string, len(in.Records))
	for i, r := range in.Records {
		if r.Key.Store == "" || r.Key.ID == "" || keys[r.Key] || r.Order < 0 {
			return plan, ErrStoreInventory
		}
		if _, err := retentionPathKey(r.Key.Store); err != nil {
			return plan, err
		}
		keys[r.Key] = true
		seen := make(map[string]bool)
		for _, u := range r.Units {
			key, err := add(u, r.Protected)
			if err != nil {
				return plan, err
			}
			if !seen[key] {
				seen[key] = true
				units[key].refs++
				recordUnits[i] = append(recordUnits[i], key)
			}
		}
	}
	for _, c := range units {
		if c.fixed {
			var err error
			plan.ProtectedBytes, err = retentionAdd(plan.ProtectedBytes, c.bytes)
			if err != nil {
				return plan, err
			}
		}
	}
	for _, u := range in.Unreferenced {
		key, err := retentionPathKey(u.Path)
		if err != nil {
			return plan, err
		}
		if c := units[key]; c != nil {
			return plan, ErrStoreInventory // Contradictory reachability evidence.
		}
		if _, err := add(u, false); err != nil {
			return plan, err
		}
	}
	plan.AfterBytes = plan.BeforeBytes
	if plan.ProtectedBytes > limit {
		for _, r := range in.Records {
			plan.Kept = append(plan.Kept, r.Key)
		}
		return plan, fmt.Errorf("%w: protected files require %d bytes, limit %d bytes", ErrStoreBudget, plan.ProtectedBytes, limit)
	}
	if plan.AfterBytes > limit {
		for _, u := range in.Unreferenced {
			plan.Reclaim = append(plan.Reclaim, u)
			plan.AfterBytes -= u.Bytes
		}
	}
	order := make([]int, 0, len(in.Records))
	for i, r := range in.Records {
		if !r.Protected {
			order = append(order, i)
		}
	}
	sort.Slice(order, func(a, b int) bool {
		x, y := in.Records[order[a]], in.Records[order[b]]
		if !x.CreatedAt.Equal(y.CreatedAt) {
			return x.CreatedAt.Before(y.CreatedAt)
		}
		if x.Key.Store != y.Key.Store {
			return x.Key.Store < y.Key.Store
		}
		if x.Order != y.Order {
			return x.Order < y.Order
		}
		return x.Key.ID < y.Key.ID
	})
	expired := make(map[RetentionKey]bool)
	for _, i := range order {
		if plan.AfterBytes <= limit {
			break
		}
		r := in.Records[i]
		expired[r.Key] = true
		plan.Expired = append(plan.Expired, r.Key)
		for _, key := range recordUnits[i] {
			c := units[key]
			c.refs--
			if c.refs == 0 && !c.fixed {
				plan.AfterBytes -= c.bytes
			}
		}
	}
	for _, r := range in.Records {
		if !expired[r.Key] {
			plan.Kept = append(plan.Kept, r.Key)
		}
	}
	if plan.AfterBytes > limit {
		return plan, fmt.Errorf("%w: no safe retention plan fits %d bytes", ErrStoreBudget, limit)
	}
	return plan, nil
}
