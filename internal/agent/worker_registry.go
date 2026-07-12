package agent

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"supercli/internal/tools/core"
)

// Worker is a persistent child loop created by the task tool. It keeps its
// own message history so the coordinator can continue it with send_message
// instead of dragging that context into the main chat.
type Worker struct {
	ID          string
	Agent       string
	Description string
	// Model is the worker's backend model when it differs from the
	// coordinator's (config `task_model`). "" = same backend; the
	// summary line then stays in its historical single-model format.
	Model     string
	Loop      *Loop
	CreatedAt time.Time

	// Mutable run state, guarded by stateMu. The run goroutine updates
	// these mid-run while the TUI status bar and "/workers" read them via
	// Snapshot/Counts, so every access outside single-threaded setup code
	// must hold stateMu.
	UpdatedAt  time.Time
	Status     string
	LastResult string
	LastError  string
	TokensIn   int
	TokensOut  int
	Steps      int // model turns consumed across all runs

	// runMu serializes runs: runWorkerLoop holds it for the whole run so
	// a worker executes at most one prompt at a time.
	runMu sync.Mutex
	// stateMu guards the mutable state fields above. It is only ever held
	// briefly (copying fields), never across Loop/provider calls, and is
	// always acquired after runMu when both are needed.
	stateMu sync.RWMutex

	// cancelMu guards cancel separately from runMu: runWorkerLoop holds
	// runMu for the whole run, and Stop must be callable mid-run.
	cancelMu sync.Mutex
	cancel   context.CancelFunc
	stopped  bool
}

// setCancel installs the cancel function for the current run.
func (w *Worker) setCancel(c context.CancelFunc) {
	w.cancelMu.Lock()
	w.cancel = c
	w.stopped = false
	w.cancelMu.Unlock()
}

// clearCancel removes the cancel function after a run ends and reports
// whether the run was stopped via Stop.
func (w *Worker) clearCancel() bool {
	w.cancelMu.Lock()
	defer w.cancelMu.Unlock()
	w.cancel = nil
	return w.stopped
}

// Stop cancels the worker's current run (if any). Returns true when a
// running worker was signalled.
func (w *Worker) Stop() bool {
	w.cancelMu.Lock()
	defer w.cancelMu.Unlock()
	if w.cancel == nil {
		return false
	}
	w.stopped = true
	w.cancel()
	w.cancel = nil
	return true
}

// Snapshot is a copy of the worker's reportable state, safe to render
// without holding locks.
type Snapshot struct {
	ID          string
	Agent       string
	Description string
	Model       string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastError   string
	TokensIn    int
	TokensOut   int
	Steps       int
}

// Snapshot returns the current reportable state. It takes stateMu (not
// runMu, which is held for the whole run) so it can be called safely from
// the TUI/"/workers" while the run goroutine updates the worker.
func (w *Worker) Snapshot() Snapshot {
	w.stateMu.RLock()
	defer w.stateMu.RUnlock()
	return Snapshot{
		ID:          w.ID,
		Agent:       w.Agent,
		Description: w.Description,
		Model:       w.Model,
		Status:      w.Status,
		CreatedAt:   w.CreatedAt,
		UpdatedAt:   w.UpdatedAt,
		LastError:   w.LastError,
		TokensIn:    w.TokensIn,
		TokensOut:   w.TokensOut,
		Steps:       w.Steps,
	}
}

// setState applies a state mutation under stateMu. The callback must only
// touch the state fields — never call back into the loop/provider while
// stateMu is held.
func (w *Worker) setState(fn func(w *Worker)) {
	w.stateMu.Lock()
	fn(w)
	w.stateMu.Unlock()
}

// status returns the current status under the state lock.
func (w *Worker) status() string {
	w.stateMu.RLock()
	defer w.stateMu.RUnlock()
	return w.Status
}

// Worker retention and concurrency defaults ("just works": empty
// config = the best version, no new toml knobs). Both are process-wide
// constants with an env escape hatch for unusual setups:
//
//   - DefaultFinishedWorkerRetention caps how many FINISHED workers
//     (done/failed/stopped) the registry keeps alive. Every worker
//     holds its whole Loop — the full conversation history — so an
//     unbounded registry is a slow memory leak in long sessions. The
//     oldest finished workers (LRU by UpdatedAt) are evicted; a compact
//     result summary is kept so /workers, send_message and task_stop
//     can say "evicted, here is what it did" instead of "unknown".
//     Override: SUPERCLI_WORKER_RETENTION.
//   - DefaultMaxActiveWorkers caps CONCURRENT active (created/running)
//     workers. Workers write files and spawn processes; an unbounded
//     fan-out from one over-eager coordinator turn can swamp a local
//     host. There is no read/write worker distinction in the codebase
//     (any worker with file tools may write), so this is a single
//     global cap — a new task over the limit fails fast with a clear
//     message instead of queueing (blocking inside a tool call would
//     stall the coordinator's whole turn). Active workers are NEVER
//     evicted. Override: SUPERCLI_MAX_ACTIVE_WORKERS.
const (
	DefaultFinishedWorkerRetention = 20
	DefaultMaxActiveWorkers        = 6
)

// evictedResultHead/Tail cap the kept LastResult of an evicted worker
// (core.HeadTail convention: head + omission marker + tail).
const (
	evictedResultHead = 300
	evictedResultTail = 100
)

// EvictedWorker is the compact snapshot kept after a finished worker
// is evicted from the registry: enough to answer "what did it do"
// without holding the Loop (and thus the whole conversation) alive.
type EvictedWorker struct {
	ID         string
	Agent      string
	Model      string
	Status     string
	LastError  string
	LastResult string // capped via core.HeadTail
	TokensIn   int
	TokensOut  int
	Steps      int
	UpdatedAt  time.Time
}

// Line renders the one-line summary used in errors and /workers.
func (e EvictedWorker) Line() string {
	s := fmt.Sprintf("%s (%s, %s) · %d steps · %d in/%d out tok",
		e.ID, e.Agent, e.Status, e.Steps, e.TokensIn, e.TokensOut)
	if e.Model != "" {
		s += " · model=" + e.Model
	}
	if e.LastError != "" {
		s += " · error: " + e.LastError
	}
	if strings.TrimSpace(e.LastResult) != "" {
		s += " · result: " + strings.TrimSpace(e.LastResult)
	}
	return s
}

// evictionSnapshot copies the fields kept after eviction, under the
// worker's state lock (same contract as Snapshot).
func (w *Worker) evictionSnapshot() EvictedWorker {
	w.stateMu.RLock()
	defer w.stateMu.RUnlock()
	return EvictedWorker{
		ID:         w.ID,
		Agent:      w.Agent,
		Model:      w.Model,
		Status:     w.Status,
		LastError:  w.LastError,
		LastResult: core.HeadTail(w.LastResult, evictedResultHead, evictedResultTail),
		TokensIn:   w.TokensIn,
		TokensOut:  w.TokensOut,
		Steps:      w.Steps,
		UpdatedAt:  w.UpdatedAt,
	}
}

// WorkerRegistry stores live/completed workers for the current process.
type WorkerRegistry struct {
	mu      sync.RWMutex
	seq     atomic.Uint64
	workers map[string]*Worker
	// evicted keeps compact summaries of finished workers pruned by the
	// retention policy, so references by id stay answerable. Guarded by mu.
	evicted map[string]EvictedWorker
	// retention/maxActive are set once at construction (env-overridable
	// constants) and read-only afterwards.
	retention int
	maxActive int
}

func NewWorkerRegistry() *WorkerRegistry {
	return &WorkerRegistry{
		workers:   make(map[string]*Worker),
		evicted:   make(map[string]EvictedWorker),
		retention: envPositiveInt("SUPERCLI_WORKER_RETENTION", DefaultFinishedWorkerRetention),
		maxActive: envPositiveInt("SUPERCLI_MAX_ACTIVE_WORKERS", DefaultMaxActiveWorkers),
	}
}

// envPositiveInt reads a positive-integer env override, falling back
// to def when unset, unparsable, or non-positive.
func envPositiveInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func (r *WorkerRegistry) Add(agentName, description string, loop *Loop) *Worker {
	if r == nil {
		return nil
	}
	w, _ := r.add(agentName, description, loop, false)
	return w
}

// TryAdd is Add with the active-worker cap enforced: when maxActive
// workers are already active (created/running) it returns a clear
// error instead of registering a new one. The task tool uses this so
// an over-eager coordinator cannot fan out unbounded concurrent
// workers; Add (no cap) stays for callers/tests that manage their own
// concurrency.
func (r *WorkerRegistry) TryAdd(agentName, description string, loop *Loop) (*Worker, error) {
	if r == nil {
		return nil, fmt.Errorf("worker registry is nil")
	}
	return r.add(agentName, description, loop, true)
}

func (r *WorkerRegistry) add(agentName, description string, loop *Loop, enforceLimit bool) (*Worker, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if enforceLimit && r.maxActive > 0 {
		if active := r.activeCountLocked(); active >= r.maxActive {
			return nil, fmt.Errorf(
				"worker limit reached: %d workers active (max %d) — wait for one to finish, stop one with task_stop, or continue an existing worker with send_message",
				active, r.maxActive)
		}
	}
	id := fmt.Sprintf("worker-%d", r.seq.Add(1))
	now := time.Now()
	w := &Worker{
		ID:          id,
		Agent:       agentName,
		Description: description,
		Loop:        loop,
		CreatedAt:   now,
		UpdatedAt:   now,
		Status:      "created",
	}
	r.workers[id] = w
	r.evictLocked()
	return w, nil
}

// activeCountLocked counts created/running workers. Caller holds mu.
func (r *WorkerRegistry) activeCountLocked() int {
	n := 0
	for _, w := range r.workers {
		switch w.status() {
		case "created", "running":
			n++
		}
	}
	return n
}

// evictLocked applies the retention policy: when more than retention
// FINISHED workers (done/failed/stopped) are held, the oldest by
// UpdatedAt are dropped from the registry and replaced by compact
// EvictedWorker summaries. Active workers are never touched. Caller
// holds mu (write). Lock order is registry mu → worker stateMu, the
// same as every other registry read path (Counts, List+Snapshot).
func (r *WorkerRegistry) evictLocked() {
	if r.retention <= 0 || r.evicted == nil {
		return
	}
	var finished []*Worker
	for _, w := range r.workers {
		switch w.status() {
		case "done", "failed", "stopped":
			finished = append(finished, w)
		}
	}
	if len(finished) <= r.retention {
		return
	}
	sort.Slice(finished, func(i, j int) bool {
		si, sj := finished[i].Snapshot(), finished[j].Snapshot()
		if si.UpdatedAt.Equal(sj.UpdatedAt) {
			return workerSeq(si.ID) < workerSeq(sj.ID)
		}
		return si.UpdatedAt.Before(sj.UpdatedAt)
	})
	for _, w := range finished[:len(finished)-r.retention] {
		r.evicted[w.ID] = w.evictionSnapshot()
		delete(r.workers, w.ID)
	}
}

// Evicted returns the kept summary of a worker pruned by the
// retention policy, if any.
func (r *WorkerRegistry) Evicted(id string) (EvictedWorker, bool) {
	if r == nil {
		return EvictedWorker{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.evicted[id]
	return e, ok
}

// EvictedList returns all kept eviction summaries ordered by numeric
// worker id, for the /workers panel.
func (r *WorkerRegistry) EvictedList() []EvictedWorker {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	out := make([]EvictedWorker, 0, len(r.evicted))
	for _, e := range r.evicted {
		out = append(out, e)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return workerSeq(out[i].ID) < workerSeq(out[j].ID)
	})
	return out
}

func (r *WorkerRegistry) Get(id string) (*Worker, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	w, ok := r.workers[id]
	return w, ok
}

// List returns all workers ordered by their numeric id (worker-1,
// worker-2, ...).
func (r *WorkerRegistry) List() []*Worker {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	out := make([]*Worker, 0, len(r.workers))
	for _, w := range r.workers {
		out = append(out, w)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return workerSeq(out[i].ID) < workerSeq(out[j].ID)
	})
	return out
}

// Stop cancels the worker with the given id. The error explains why a
// stop did not happen (unknown id, not running).
func (r *WorkerRegistry) Stop(id string) error {
	w, ok := r.Get(id)
	if !ok {
		if e, evicted := r.Evicted(id); evicted {
			return fmt.Errorf("worker %s was evicted (finished workers beyond retention are pruned); it is not running. Summary: %s", id, e.Line())
		}
		return fmt.Errorf("unknown worker %q", id)
	}
	if !w.Stop() {
		return fmt.Errorf("worker %s is not running (status: %s)", id, w.status())
	}
	return nil
}

func workerSeq(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "worker-"))
	return n
}

// WorkerCounts summarizes the registry by status for the status bar.
// Running counts workers currently executing; Done/Failed/Stopped are
// terminal; Total is everything ever spawned this process.
type WorkerCounts struct {
	Running int
	Done    int
	Failed  int
	Stopped int
	Total   int
}

// Counts tallies workers by status. Safe to call from the TUI render
// path; it takes the registry read lock and each worker's state read
// lock (same contract as Snapshot).
func (r *WorkerRegistry) Counts() WorkerCounts {
	var c WorkerCounts
	if r == nil {
		return c
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, w := range r.workers {
		c.Total++
		switch w.status() {
		case "running", "created":
			c.Running++
		case "done":
			c.Done++
		case "failed":
			c.Failed++
		case "stopped":
			c.Stopped++
		}
	}
	// Evicted workers were spawned this process too: keep Total (and the
	// terminal buckets) meaning "everything ever spawned", not "still held".
	for _, e := range r.evicted {
		c.Total++
		switch e.Status {
		case "done":
			c.Done++
		case "failed":
			c.Failed++
		case "stopped":
			c.Stopped++
		}
	}
	return c
}

// StatusTile renders a one-line worker summary for the status bar, or
// "" when nothing has been spawned yet. Example: "2 running · 1 done".
func (c WorkerCounts) StatusTile() string {
	if c.Total == 0 {
		return ""
	}
	var seg []string
	if c.Running > 0 {
		seg = append(seg, fmt.Sprintf("%d running", c.Running))
	}
	if c.Done > 0 {
		seg = append(seg, fmt.Sprintf("%d done", c.Done))
	}
	if c.Failed > 0 {
		seg = append(seg, fmt.Sprintf("%d failed", c.Failed))
	}
	if c.Stopped > 0 {
		seg = append(seg, fmt.Sprintf("%d stopped", c.Stopped))
	}
	if len(seg) == 0 {
		return ""
	}
	return strings.Join(seg, " · ")
}
