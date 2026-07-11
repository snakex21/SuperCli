package agent

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

// WorkerRegistry stores live/completed workers for the current process.
type WorkerRegistry struct {
	mu      sync.RWMutex
	seq     atomic.Uint64
	workers map[string]*Worker
}

func NewWorkerRegistry() *WorkerRegistry {
	return &WorkerRegistry{workers: make(map[string]*Worker)}
}

func (r *WorkerRegistry) Add(agentName, description string, loop *Loop) *Worker {
	if r == nil {
		return nil
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
	r.mu.Lock()
	r.workers[id] = w
	r.mu.Unlock()
	return w
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
