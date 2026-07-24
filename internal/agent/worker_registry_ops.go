package agent

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

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
