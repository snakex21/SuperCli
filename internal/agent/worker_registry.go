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
	Loop        *Loop
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Status      string
	LastResult  string
	LastError   string
	TokensIn    int
	TokensOut   int

	mu sync.Mutex

	// cancelMu guards cancel separately from mu: runWorkerLoop holds mu
	// for the whole run, and Stop must be callable mid-run.
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
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastError   string
	TokensIn    int
	TokensOut   int
}

// Snapshot returns the current reportable state. It deliberately does NOT
// take w.mu (held for the whole run); fields are read racily but are only
// used for display.
func (w *Worker) Snapshot() Snapshot {
	return Snapshot{
		ID:          w.ID,
		Agent:       w.Agent,
		Description: w.Description,
		Status:      w.Status,
		CreatedAt:   w.CreatedAt,
		UpdatedAt:   w.UpdatedAt,
		LastError:   w.LastError,
		TokensIn:    w.TokensIn,
		TokensOut:   w.TokensOut,
	}
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
		return fmt.Errorf("worker %s is not running (status: %s)", id, w.Status)
	}
	return nil
}

func workerSeq(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "worker-"))
	return n
}
