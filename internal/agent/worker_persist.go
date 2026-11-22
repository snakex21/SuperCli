package agent

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"supercli/internal/llm"
)

// Resumable workers. A worker's own conversation (its transcript, never the
// parent chat) and identity are saved to the session store after creation and
// after every run. A worker that stopped, failed, was interrupted, was evicted
// from memory or outlived its process can then be continued by id with its full
// context: send_message rebuilds the loop from the saved transcript and the
// follow-up becomes a new turn appended to it.

// WorkerRecord is the persisted form of a worker.
type WorkerRecord struct {
	SessionID   string
	ID          string
	Agent       string
	Description string
	Model       string
	BaseDir     string
	Status      string
	LastError   string
	Runs        int
	Messages    []llm.Message
	UpdatedAt   time.Time
}

// WorkerStore persists workers per conversation (session store adapter).
type WorkerStore interface {
	SaveWorker(ctx context.Context, rec WorkerRecord) error
	LoadWorker(ctx context.Context, sessionID, id string) (WorkerRecord, bool, error)
	ListWorkers(ctx context.Context, sessionID string) ([]WorkerRecord, error)
	SetWorkerStatus(ctx context.Context, sessionID, id, status, reason string) error
}

// workerNotStartedReason is the saved, user-visible reason of a worker whose
// first run never began.
func workerNotStartedReason(err error) string {
	return fmt.Sprintf("did not start: %v; use Resume/send_message to retry it with its original task", err)
}

// Interrupted is the definite state of a saved worker whose runner is gone
// (app restart, crash): it is not running and can be resumed.
const WorkerStatusInterrupted = "interrupted"

// SetStore enables worker persistence. existingIDs are ids already saved by
// earlier processes; new ids are allocated above them so a resumable worker's
// id never names a different worker.
func (r *WorkerRegistry) SetStore(store WorkerStore, existingIDs []string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.store = store
	r.mu.Unlock()
	for _, id := range existingIDs {
		n, err := strconv.ParseUint(strings.TrimPrefix(id, "worker-"), 10, 64)
		if err != nil {
			continue
		}
		for {
			cur := r.seq.Load()
			if n <= cur || r.seq.CompareAndSwap(cur, n) {
				break
			}
		}
	}
}

// Store returns the persistence backend, if any.
func (r *WorkerRegistry) Store() WorkerStore {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.store
}

func persistableMessages(msgs []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == llm.RoleSystem {
			continue // rebuilt from the agent spec on resume
		}
		out = append(out, m)
	}
	return out
}

// persist saves the worker. With runMu held (or before the first run) the
// loop history is stable.
func (r *WorkerRegistry) persist(w *Worker) {
	store := r.Store()
	if store == nil || w == nil || w.sessionID == "" {
		return
	}
	s := w.Snapshot()
	rec := WorkerRecord{SessionID: w.sessionID, ID: w.ID, Agent: w.Agent, Description: w.Description, Model: s.Model,
		BaseDir: w.baseDir, Status: s.Status, LastError: s.LastError, Runs: s.Runs, UpdatedAt: time.Now()}
	if w.Loop != nil {
		rec.Messages = persistableMessages(w.Loop.Messages)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.SaveWorker(ctx, rec); err != nil {
		log.Printf("worker %s: save transcript: %v", w.ID, err)
	}
}

// sessionIDOf names the parent conversation a worker belongs to.
func sessionIDOf(l *Loop) string {
	if l == nil {
		return ""
	}
	if w, ok := l.writer.(interface{ SessionID() string }); ok {
		return w.SessionID()
	}
	return ""
}

// addRestored registers a saved worker under its original id.
func (r *WorkerRegistry) addRestored(rec WorkerRecord, loop *Loop) (*Worker, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w, ok := r.workers[rec.ID]; ok {
		return w, nil // restored concurrently
	}
	status := rec.Status
	switch status {
	case "created", "running", "":
		status = WorkerStatusInterrupted
	}
	now := time.Now()
	w := &Worker{ID: rec.ID, Agent: rec.Agent, Description: rec.Description, Model: rec.Model, Loop: loop,
		CreatedAt: now, UpdatedAt: now, Status: status, LastError: rec.LastError, Runs: rec.Runs,
		sessionID: rec.SessionID, baseDir: rec.BaseDir, registry: r}
	delete(r.evicted, rec.ID)
	r.workers[rec.ID] = w
	r.evictLocked()
	return w, nil
}

// restoreWorker rebuilds a saved worker of the parent's conversation.
func (a *AgentTool) restoreWorker(ctx context.Context, id string) (*Worker, error) {
	store := a.Workers.Store()
	sid := sessionIDOf(a.ParentLoop)
	if store == nil || sid == "" {
		return nil, fmt.Errorf("unknown worker %q", id)
	}
	rec, ok, err := store.LoadWorker(ctx, sid, id)
	if err != nil {
		return nil, fmt.Errorf("worker %s: read saved transcript: %w", id, err)
	}
	if !ok {
		return nil, fmt.Errorf("unknown worker %q", id)
	}
	spec, ok := a.Registry.Get(rec.Agent)
	if !ok {
		return nil, fmt.Errorf("worker %s: unknown agent kind %q", id, rec.Agent)
	}
	system, instructions, instructionsForRun := a.workerInstructions(ctx, spec.System)
	maxSteps := spec.MaxSteps
	if maxSteps <= 0 {
		maxSteps = a.MaxSteps
	}
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}
	var budget CreditTracker
	if a.MaxTokens > 0 {
		budget = newTokenBudget(a.MaxTokens)
	}
	prov := llm.Provider(a.workerProvider(ctx))
	if rec.Agent == advisorAgentKind && a.AdvisorProvider != nil {
		prov = a.AdvisorProvider
	}
	thin, stable, hoist, baseDir := a.childLoopSettings(prov)
	if rec.BaseDir != "" {
		baseDir = rec.BaseDir
	}
	var deferred []string
	if !thin {
		deferred = spec.DeferredTools
	}
	outputSource := a.BaseRegistry
	cfg := LoopConfig{Provider: prov, Caps: a.Caps, System: system, UserInstructions: instructions, UserInstructionsForRun: instructionsForRun,
		SkipImplementationHint: spec.SkipImplementationHint, MaxSteps: maxSteps, InitialMessages: rec.Messages,
		ThinTools: thin, StableToolset: stable, CatalogHoist: hoist, BaseDir: baseDir, CreditTracker: budget,
		PrefillProfiles: a.PrefillProfiles}
	if p := a.ParentLoop; p != nil {
		if p.registry != nil {
			outputSource = p.registry
		}
		cfg.ReplaceContextTail = p.tailReplace
		cfg.WindowFor, cfg.ContextWindowFor, cfg.ScopedContextWindowFor = p.windowFor, p.contextWindowFor, p.scopedWindowFor
		cfg.ContextProvider, cfg.Summarizer, cfg.ToolOutputs = p.contextProvider, p.summarizer, p.toolOutputs
		cfg.ImageStore, _ = p.writer.(imageExternalizer)
		cfg.LearnLimit, cfg.PruneProtectTokens = p.learnLimit, p.pruneProtect
		if cfg.PrefillProfiles == nil {
			cfg.PrefillProfiles = p.prefillProfiles
		}
	}
	if a.WorkerProvider != nil && prov == a.WorkerProvider && a.WorkerContextProvider != "" {
		cfg.ContextProvider = a.WorkerContextProvider
	}
	cfg.Registry = restrictedRegistrySharingOutputs(a.BaseRegistry, outputSource, spec.AllowedTools, deferred...)
	if thin {
		ensureWorkerDiscovery(cfg.Registry)
	}
	loop, err := a.NewLoop(cfg)
	if err != nil {
		return nil, fmt.Errorf("worker %s: rebuild loop: %w", id, err)
	}
	w, err := a.Workers.addRestored(rec, loop)
	if err != nil {
		return nil, err
	}
	effort := a.WorkerReasoningEffort
	if rec.Agent == advisorAgentKind {
		effort = a.AdvisorReasoningEffort
	}
	w.setState(func(w *Worker) { w.reasoningEffort = effort })
	if a.ParentLoop != nil {
		w.progress = func(ev WorkerProgressEvent) { a.ParentLoop.Emit(ev) }
	}
	a.installReview(w)
	return w, nil
}

// ReconcileSavedWorkers turns saved workers that claim to be active but have
// no live runner in this process into a definite, resumable state. It
// returns the reconciled records (for notifications). Call on load.
func (r *WorkerRegistry) ReconcileSavedWorkers(ctx context.Context, sessionID string) ([]WorkerRecord, error) {
	store := r.Store()
	if store == nil || sessionID == "" {
		return nil, nil
	}
	recs, err := store.ListWorkers(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	var changed []WorkerRecord
	for _, rec := range recs {
		if rec.Status != "created" && rec.Status != "running" {
			continue
		}
		if w, ok := r.Get(rec.ID); ok && w.Snapshot().SessionID == sessionID {
			// The registry is process-wide: only this conversation's worker
			// with the same id proves a live runner.
			if s := w.status(); s == "created" || s == "running" {
				continue // genuinely live in this process
			}
		}
		reason := "runner ended without a final report (app closed or crashed); resume to continue"
		if err := store.SetWorkerStatus(ctx, sessionID, rec.ID, WorkerStatusInterrupted, reason); err != nil {
			return changed, err
		}
		rec.Status, rec.LastError = WorkerStatusInterrupted, reason
		changed = append(changed, rec)
	}
	return changed, nil
}
