package agent

import (
	"context"
	"fmt"
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
	ToolNames  []string

	// progress is installed by AgentTool before the first run. It is immutable
	// afterwards and emits best-effort UI events through the parent loop.
	progress func(WorkerProgressEvent)

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
	ToolNames   []string
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
		ToolNames:   append([]string(nil), w.ToolNames...),
	}
}

func (w *Worker) emitProgress(ev WorkerProgressEvent) {
	if w == nil || w.progress == nil {
		return
	}
	ev.TaskID = w.ID
	ev.Agent = w.Agent
	w.progress(ev)
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
