package checkpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const storeUsageName = ".checkpoint-usage.json"
const storeUsageMaxJSON = 256 // Protocol size bound, not a store-growth estimate.

var ErrStoreUsageCensus = errors.New("checkpoint usage requires fresh census")

// A bounded process-local first-completion guard, shared across cached Managers.
// A hash collision may cause another census; it cannot authorize a stale total.
// No history, PID, callback, Manager, request context or growing root map is kept.
var storeUsageVerified = [256]struct {
	sync.Mutex
	key string
}{}

func storeUsageTrust(key string, mark bool) bool {
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	s := &storeUsageVerified[h.Sum32()%uint32(len(storeUsageVerified))]
	s.Lock()
	defer s.Unlock()
	if mark {
		s.key = key
	}
	return s.key == key
}

type storeUsageState struct {
	Version int    `json:"version"`
	Upper   int64  `json:"upper_bytes"`
	Epoch   uint64 `json:"epoch"`
	Dirty   bool   `json:"dirty"`
	// Managed policy's last complete-census floor allowance. Zero means the
	// strict base limit applies. No root/graph/history cache is retained.
	ProtectedFloor int64 `json:"protected_floor_bytes,omitempty"`
}

// The existing StoreGate must remain held across every Locked method and all
// checkpoint writes in one receipt. This helper never acquires a nested gate.
type StoreUsageCounter struct {
	dataDir, key string
}

func NewStoreUsageCounter(gate *StoreGate) (*StoreUsageCounter, error) {
	if gate == nil || gate.path == "" {
		return nil, ErrStoreInventory
	}
	return &StoreUsageCounter{dataDir: filepath.Dir(gate.path), key: gate.path}, nil
}

func (c *StoreUsageCounter) readLocked() (storeUsageState, error) {
	var s storeUsageState
	path := filepath.Join(c.dataDir, storeUsageName)
	if err := retentionSafePath(c.dataDir, path); err != nil {
		return s, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return s, ErrStoreUsageCensus
	}
	if err != nil {
		return s, err
	}
	if !info.Mode().IsRegular() {
		return s, ErrStoreInventory
	}
	if info.Size() > storeUsageMaxJSON {
		return s, ErrStoreUsageCensus
	}
	f, err := os.Open(path)
	if err != nil {
		return s, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, storeUsageMaxJSON+1))
	if err != nil {
		return s, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if len(data) > storeUsageMaxJSON || dec.Decode(&s) != nil || s.Version != 1 || s.Upper < 0 || s.Epoch == 0 || s.ProtectedFloor < 0 || s.ProtectedFloor > s.Upper {
		return storeUsageState{}, ErrStoreUsageCensus
	}
	if dec.Decode(new(any)) != io.EOF {
		return storeUsageState{}, ErrStoreUsageCensus
	}
	return s, nil
}

func (c *StoreUsageCounter) writeLocked(s storeUsageState) error {
	if s.Version != 1 || s.Upper < 0 || s.Epoch == 0 || s.ProtectedFloor < 0 || s.ProtectedFloor > s.Upper {
		return ErrStoreInventory
	}
	data, err := json.Marshal(s)
	if err != nil || len(data) > storeUsageMaxJSON {
		return ErrStoreInventory
	}
	// Shared collector primitive: Sync staging, atomic durable promotion in the
	// portable data directory; Windows MoveFileEx WRITE_THROUGH/POSIX dir.Sync.
	return retentionAtomicWrite(c.dataDir, filepath.Join(c.dataDir, storeUsageName), data)
}

type StoreUsageWrite struct {
	counter *StoreUsageCounter
	state   storeUsageState
	blobs   int64
	sticky  bool
	done    bool
	invalid error
}

// BeginLocked MUST precede the first checkpoint write, including init/ref/temp
// creation. No clean read permits writes until the dirty marker is durable.
// A missing/malformed regular ledger is safely repaired as unknown+dirty, not
// treated as zero usage. A canceled/error path intentionally leaves it dirty.
func (c *StoreUsageCounter) BeginLocked(ctx context.Context) (*StoreUsageWrite, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := c.readLocked()
	if err != nil {
		if !errors.Is(err, ErrStoreUsageCensus) {
			return nil, err
		}
		s = storeUsageState{Version: 1, Dirty: true}
	}
	if s.Epoch == ^uint64(0) {
		return nil, ErrStoreUsageCensus
	}
	w := &StoreUsageWrite{counter: c, sticky: s.Dirty}
	s.Dirty, s.Epoch = true, s.Epoch+1
	if err := c.writeLocked(s); err != nil {
		return nil, err
	}
	w.state = s
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return w, nil
}

// AddPublishedBlobBytes receives compressed size ONLY after a new object was
// promoted. Existing OIDs add zero. Use output.Stat or a counting writer already
// below zlib; never reread a blob or walk the object database here.
func (w *StoreUsageWrite) AddPublishedBlobBytes(n int64) error {
	if w == nil || w.counter == nil || w.done {
		return ErrStoreInventory
	}
	if w.invalid != nil {
		return w.invalid
	}
	next, err := retentionAdd(w.blobs, n)
	if err != nil {
		w.invalid = err // Accounting failure cannot subsequently become clean.
		return err
	}
	w.blobs = next
	return nil
}

// RequireCensus handles known unbounded/untracked writes (notably git init's
// templates/config or unexpected reflog/index leftovers). No guessed allowance
// is used. This transaction's existing durable marker stays sticky on success.
func (w *StoreUsageWrite) RequireCensus() error {
	if w == nil || w.counter == nil || w.done {
		return ErrStoreInventory
	}
	w.sticky = true
	return nil
}

// All components are nonnegative final-growth upper bounds. Charge complete
// replacement sizes, never subtract their former sizes or confirmed deletes.
// Tree/commit may use measured new compressed sizes or the documented bound;
// metadata uses encoded len already computed for save, refs use 41 per write.
type StoreUsageGrowth struct {
	TreeCommitBytes int64
	RefBytes        int64
	MetadataBytes   int64
	OtherBytes      int64 // init/reflog/config/failed cleanup paths, if known bounded.
}

func (g StoreUsageGrowth) total(blobs int64) (int64, error) {
	total := blobs
	for _, n := range []int64{g.TreeCommitBytes, g.RefBytes, g.MetadataBytes, g.OtherBytes} {
		var err error
		total, err = retentionAdd(total, n)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

// FinishLocked runs only after successful known writes AND staging cleanup.
// Any failure, unknown writer, overflow or generation mismatch leaves Dirty.
// In particular a second process cannot wash out a prior crashed writer's debt.
func (w *StoreUsageWrite) FinishLocked(ctx context.Context, growth StoreUsageGrowth) (err error) {
	if w == nil || w.counter == nil || w.done {
		return ErrStoreInventory
	}
	if w.invalid != nil {
		return w.invalid
	}
	defer func() {
		if err != nil {
			w.invalid = err // A failed receipt cannot be retried into clean state.
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := w.counter.readLocked()
	if err != nil || current != w.state {
		return errors.Join(ErrStoreUsageCensus, err)
	}
	delta, err := growth.total(w.blobs)
	if err != nil {
		return err
	}
	current.Upper, err = retentionAdd(current.Upper, delta)
	if err != nil || current.Epoch == ^uint64(0) {
		return errors.Join(ErrStoreUsageCensus, err)
	}
	current.Dirty, current.Epoch = w.sticky, current.Epoch+1
	if err := w.counter.writeLocked(current); err != nil {
		return err
	}
	w.done = true
	return nil
}

type StoreUsageCompletion struct {
	UpperBytes int64
	Collected  bool
}

// CompleteLocked has a tiny read-only fastpath. collect is invoked only on the
// first completion in this process, prior dirty/missing/invalid state or pressure.
// collect MUST finish a complete fresh census/pressure-only collection under
// this SAME gate, returning measured final file bytes only after full success.
// No strict admission, after reserve, timer, goroutine, polling or model hook.
func (c *StoreUsageCounter) CompleteLocked(ctx context.Context, limit int64, collect func(context.Context) (int64, error)) (StoreUsageCompletion, error) {
	var result StoreUsageCompletion
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if limit <= 0 {
		return result, ErrStoreInventory
	}
	s, err := c.readLocked()
	if err != nil && !errors.Is(err, ErrStoreUsageCensus) {
		return result, err
	}
	if err == nil {
		result.UpperBytes = s.Upper
	}
	if err == nil && !s.Dirty && s.Upper <= limit && storeUsageTrust(c.key, false) {
		return result, nil
	}
	if collect == nil {
		return result, ErrStoreUsageCensus
	}
	// Collection itself rewrites metadata/refs and can crash too.
	w, err := c.BeginLocked(ctx)
	if err != nil {
		return result, err
	}
	measured, err := collect(ctx)
	result.Collected = true
	if err != nil {
		return result, err
	}
	if measured < 0 || w.state.Epoch == ^uint64(0) {
		return result, ErrStoreInventory
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	fresh, err := c.readLocked()
	if err != nil || fresh != w.state {
		return result, errors.Join(ErrStoreUsageCensus, err)
	}
	fresh.Upper, fresh.Dirty, fresh.Epoch = measured, false, fresh.Epoch+1
	fresh.ProtectedFloor = 0 // Strict census never grants a floor exception.
	if err := c.writeLocked(fresh); err != nil {
		return result, err
	}
	w.done = true
	storeUsageTrust(c.key, true)
	result.UpperBytes = measured
	if measured > limit {
		return result, fmt.Errorf("%w: protected store is %d bytes", ErrStoreBudget, measured)
	}
	return result, nil
}
