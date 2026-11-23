package agent

import (
	"context"

	"supercli/internal/llm"
)

// Worker settings follow the user (after 1.0.39). A worker kept the model
// and reasoning effort it was started with: when its backend hit a limit or
// misbehaved and the user picked another worker model or reasoning level,
// resuming it still ran the old one, and only new workers got the change.
// Before each next run (resume, retry, follow-up) the worker now takes the
// current worker settings - the user's latest choice wins over whatever the
// worker was started with. A running worker is never swapped mid-run: the
// change applies at its next run.

// refreshIfIdle runs refresh when the worker is idle (its run lock is free)
// and reports whether it did. A running worker keeps its model and effort
// until its next run.
func refreshIfIdle(ctx context.Context, w *Worker, refresh func(context.Context, *Worker)) bool {
	if w == nil || refresh == nil || !w.runMu.TryLock() {
		return false
	}
	defer w.runMu.Unlock()
	refresh(ctx, w)
	return true
}

// refreshWorkerSettingsLocked needs w.runMu held by the caller.
func (a *AgentTool) refreshWorkerSettingsLocked(ctx context.Context, w *Worker) {
	if a == nil || w == nil || w.Loop == nil {
		return
	}
	effort := a.WorkerReasoningEffort
	prov := a.workerProvider(ctx)
	contextProvider := a.WorkerContextProvider
	if a.WorkerProvider == nil || !sameProviderInstance(prov, a.WorkerProvider) {
		// The worker runs on the coordinator's backend (no worker model set,
		// or the worker backend is unreachable).
		contextProvider = ""
		if a.ParentLoop != nil {
			contextProvider = a.ParentLoop.contextProvider
		}
	}
	if w.Agent == advisorAgentKind {
		effort = a.AdvisorReasoningEffort
		if a.AdvisorProvider != nil {
			prov, contextProvider = a.AdvisorProvider, ""
		}
	}
	if prov != nil && !sameProviderInstance(prov, w.Loop.provider) {
		w.Loop.SetModel(prov)
		if contextProvider != "" {
			w.Loop.SetContextProvider(contextProvider)
		}
		model := ""
		if !sameProviderInstance(prov, a.coordinatorProvider()) {
			model = prov.Name()
		}
		w.setState(func(w *Worker) { w.Model = model })
	}
	w.setState(func(w *Worker) { w.reasoningEffort = effort })
}

// workerRunModel is the model the worker's next run uses (for tests and the
// status line).
func workerRunModel(w *Worker) string {
	if w == nil || w.Loop == nil {
		return ""
	}
	return llm.ProviderModelName(w.Loop.provider)
}
