package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	"supercli/internal/tools"
	toolcore "supercli/internal/tools/core"
)

type SendMessageTool struct {
	Workers *WorkerRegistry
	// Restore rebuilds a saved worker that is not in memory (optional).
	Restore func(context.Context, string) (*Worker, error)
	// CanBackground reports whether continuations run detached from the
	// coordinator's turn (the host wakes the conversation for reports).
	CanBackground func() bool
	// StartBackground starts a continuation whose runMu the caller holds and
	// whose run the registry has admitted; it owns both from then on.
	StartBackground func(context.Context, *Worker, string) error
}

func NewSendMessageTool(workers *WorkerRegistry) *SendMessageTool {
	if workers == nil {
		workers = NewWorkerRegistry()
	}
	return &SendMessageTool{Workers: workers}
}

// NewSendMessageToolFor binds continuation to an AgentTool, which can also
// resume saved workers (after eviction, interruption or an app restart).
func NewSendMessageToolFor(a *AgentTool) *SendMessageTool {
	s := NewSendMessageTool(a.Workers)
	s.Restore = a.restoreWorker
	// Only where finished reports wake the conversation (GUI, TUI with
	// background_wake on). Elsewhere the follow-up keeps blocking, so no
	// report can be stranded.
	s.CanBackground = a.backgroundDelegation
	s.StartBackground = a.startBackgroundContinuation
	return s
}

func (s *SendMessageTool) Spec() tools.Tool {
	return tools.Tool{
		Name:        "send_message",
		Description: "Continue a finished task worker by ID, or use mode=steer to queue a correction in its current run. Steering returns a receipt without starting another run; delivery or rejection is reported with worker progress.",
		Schema: `{
			"type":"object",
			"required":["to","message"],
			"properties":{
				"to":{"type":"string","description":"worker id returned by task, e.g. worker-1"},
				"message":{"type":"string","description":"self-contained follow-up instruction for that worker"},
				"mode":{"type":"string","enum":["continue","steer"],"description":"continue (default) resumes a finished worker; steer queues an instruction only in an already running worker"}
			}
		}`,
		Fn: s.execute,
	}
}

type sendMessageArgs struct {
	To      string `json:"to"`
	Message string `json:"message"`
	Mode    string `json:"mode"`
}

func (s *SendMessageTool) execute(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
	var args sendMessageArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return tools.Result{Err: fmt.Errorf("send_message: bad args: %w", err)}, nil
	}
	args.To = strings.TrimSpace(args.To)
	args.Message = strings.TrimSpace(args.Message)
	args.Mode = strings.TrimSpace(args.Mode)
	if args.Mode != "" && args.Mode != "continue" && args.Mode != "steer" {
		return tools.Result{Err: fmt.Errorf("send_message: mode must be continue or steer")}, nil
	}
	if args.To == "" {
		return tools.Result{Err: fmt.Errorf("send_message: to is required")}, nil
	}
	if args.Message == "" {
		return tools.Result{Err: fmt.Errorf("send_message: message is required")}, nil
	}
	w, ok := s.Workers.Get(args.To)
	if !ok && s.Restore != nil {
		// Saved transcript: an interrupted, evicted or previous-process worker
		// resumes with its full context under the same id.
		if restored, err := s.Restore(ctx, args.To); err == nil {
			w, ok = restored, true
		}
	}
	if !ok {
		// An evicted worker's Loop (its conversation) is gone, but the kept
		// summary lets the coordinator learn what it did instead of a dead
		// "unknown worker".
		if e, evicted := s.Workers.Evicted(args.To); evicted {
			return tools.Result{Err: fmt.Errorf(
				"send_message: worker %s was evicted (finished workers beyond retention are pruned; its context is gone — start a new task instead). Summary: %s",
				args.To, e.Line())}, nil
		}
		return tools.Result{Err: fmt.Errorf("send_message: unknown worker %q", args.To)}, nil
	}

	if args.Mode == "steer" {
		if err := ctx.Err(); err != nil {
			return tools.Result{Err: err}, nil
		}
		if w.status() == "created" {
			return tools.Result{Err: fmt.Errorf("worker %s is already running or has not started; its original task must start before steering", w.ID)}, nil
		}
		if w.status() != "running" || w.Loop == nil {
			return tools.Result{Err: fmt.Errorf("send_message: worker %s is not running; use send_message without mode=steer to continue it", w.ID)}, nil
		}
		id, err := w.Loop.queueInterjection(args.Message, true)
		if err != nil {
			return tools.Result{Err: fmt.Errorf("send_message: worker %s: %w", w.ID, err)}, nil
		}
		return tools.Result{Text: fmt.Sprintf("Steering queued for worker %s in its current run (receipt %s). Delivery or rejection will be reported in worker progress; no new run was started.", w.ID, id)}, nil
	}
	message := args.Message
	if snap := w.Snapshot(); snap.Runs == 0 && snap.Status == "failed" && strings.HasPrefix(snap.LastError, "did not start") && strings.TrimSpace(w.Description) != "" {
		// A worker that failed before its first run has no saved conversation:
		// retrying it must carry the original task, not only the follow-up.
		message = "Your original task (it never started: " + snap.LastError + "):\n" + w.Description +
			"\n\nCoordinator follow-up:\n" + args.Message
	}
	if s.CanBackground != nil && s.StartBackground != nil && s.CanBackground() {
		// A continuation can run for many minutes. Run synchronously, it held
		// the coordinator's turn (an automatic wake-up turn too) the whole
		// time: the conversation looked busy while the coordinator only
		// waited, and the user's Stop or next message canceled the worker.
		//
		// Taking runMu and admitting the run (status, worker limits) happen
		// here, synchronously and atomically: of two parallel follow-ups one
		// starts, the other gets "already running" as its tool result - never
		// a late background report that would wake the conversation.
		if !w.runMu.TryLock() {
			return tools.Result{Err: fmt.Errorf("worker %s is already running; its current task must finish before a follow-up (use mode=steer to correct it)", w.ID)}, nil
		}
		before := w.Snapshot()
		if err := s.Workers.startContinuation(w); err != nil {
			w.runMu.Unlock()
			return tools.Result{Err: fmt.Errorf("send_message: %w", err)}, nil
		}
		if err := s.StartBackground(ctx, w, message); err != nil {
			w.setState(func(w *Worker) { w.Status, w.LastError = before.Status, before.LastError })
			w.runMu.Unlock()
			return tools.Result{Err: fmt.Errorf("send_message: %s did not start (background checkpoint binding): %w", w.ID, err)}, nil
		}
		return tools.Result{Text: backgroundRunningNotice(w, "continued in background")}, nil
	}
	handoff, err := runWorkerInvocation(llm.WithCallReason(ctx, llm.ReasonResume), w, message, s.Workers)
	return handoff.result(w, err), nil
}

// backgroundRunningNotice is the immediate result of a detached worker run.
func backgroundRunningNotice(w *Worker, what string) string {
	return fmt.Sprintf(`<task-notification>
<task-id>%s</task-id>
<agent>%s</agent>
<status>running</status>
<summary>%s %s</summary>
<continuation>Results arrive automatically. Continue independent work, or end this response to wait. Do not restart this task or use its worker ID with read_output; use send_message only for new instructions.</continuation>
</task-notification>`, w.ID, w.Agent, w.Agent, what)
}

func runWorkerLoop(ctx context.Context, w *Worker, prompt string) (string, error) {
	return runWorkerLoopInRegistry(ctx, w, prompt, nil)
}

func runWorkerLoopInRegistry(ctx context.Context, w *Worker, prompt string, workers *WorkerRegistry) (string, error) {
	handoff, err := runWorkerInvocation(ctx, w, prompt, workers)
	return handoff.report, err
}

// runWorkerInvocation captures the completed invocation before allowing a new
// run. Callers must deliver this handoff, not snapshot the mutable worker again.
func runWorkerInvocation(ctx context.Context, w *Worker, prompt string, workers *WorkerRegistry) (workerHandoff, error) {
	firstRun := workers == nil && w.status() == "created"
	if firstRun {
		// The worker's own first run. A concurrent send_message may hold runMu
		// only briefly (it refuses a not-yet-started worker), so wait for it
		// instead of failing: a TryLock miss here used to leave the worker
		// "created" forever, with no runner.
		w.runMu.Lock()
	} else if !w.runMu.TryLock() {
		return prepareWorkerHandoff(w, ""), fmt.Errorf("worker %s is already running; its current task must finish before a follow-up", w.ID)
	}
	defer w.runMu.Unlock()
	return runWorkerInvocationHeld(ctx, w, prompt, workers, firstRun)
}

// runWorkerInvocationHeld is runWorkerInvocation for a caller that already
// holds w.runMu (and has admitted the run); it does not unlock.
func runWorkerInvocationHeld(ctx context.Context, w *Worker, prompt string, workers *WorkerRegistry, firstRun bool) (workerHandoff, error) {
	w.setState(func(w *Worker) { w.review = "" })
	before := ""
	if w.reviewSnapshot != nil {
		before = w.reviewSnapshot(ctx, w)
	}
	text, err := runWorkerLoopLocked(ctx, w, prompt, workers)
	// A run that says "done" is checked against cheap facts (any file
	// change? the project's verify command?) while runMu is still held, so
	// the handoff below carries the verdict.
	if err == nil && w.status() == "done" && w.reviewHook != nil {
		if review := w.reviewHook(ctx, w, before); review != "" {
			w.setState(func(w *Worker) { w.review = review })
		}
	}
	if firstRun && w.status() == "created" {
		// Never started (canceled, checkpoint binding refused, ...): a definite
		// failed state with its reason, resumable, and reported to the
		// coordinator by the caller's notification path.
		if err == nil {
			err = fmt.Errorf("worker %s did not start", w.ID)
		}
		reason := workerNotStartedReason(err)
		w.setState(func(w *Worker) {
			w.Status, w.LastError, w.UpdatedAt = "failed", reason, time.Now()
		})
	}
	// Save the worker's whole conversation after every run (still holding
	// runMu, so the history is stable): it can be resumed by id later.
	w.registry.persist(w)
	// All locked-run defers (including evidence finalization and event drain)
	// have completed. Formatting and delivery can safely happen after unlock.
	return prepareWorkerHandoff(w, text), err
}

// observedWorkerActivity retains only bounded tool-call metadata. Results reuse
// their matching call's arguments, never the result output or error body.
func observedWorkerActivity(kind, callID string, call ToolCallEvent) WorkerActivity {
	at := time.Now().UnixMilli()
	const maxArgs = 1024
	args := call.Args
	if len(args) > maxArgs {
		args = args[:maxArgs]
	}
	// Drop an incomplete trailing rune (or any invalid input bytes) without
	// expanding the byte budget. Clone to avoid retaining a large call string.
	args = strings.Clone(strings.ToValidUTF8(args, ""))
	return WorkerActivity{Kind: kind, Tool: call.Name, Args: args, CallID: callID, At: at}
}

// Caller owns runMu through cancellation cleanup and handoff capture.
func runWorkerLoopLocked(ctx context.Context, w *Worker, prompt string, workers *WorkerRegistry) (string, error) {
	scope := "worker:" + w.ID
	if w.Loop != nil && w.Loop.runID != "" {
		scope += ":" + w.Loop.runID
	}
	ctx = llm.WithPromptCacheScope(ctx, scope)
	ctx = context.WithValue(ctx, workerRunKey{}, true)
	w.stateMu.RLock()
	effort := w.reasoningEffort
	w.stateMu.RUnlock()
	ctx = llm.WithIndependentReasoningEffort(ctx, effort)
	if w.Agent == advisorAgentKind {
		ctx = llm.WithPurpose(ctx, llm.PurposeAdvisor)
	} else {
		ctx = llm.WithPurpose(ctx, llm.PurposeTask)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	mutatingCapable := true
	if w.Loop != nil {
		mutatingCapable = checkpoint.HasCheckpointMutators(w.Loop.registry)
	}
	borrow, borrowErr := checkpoint.BorrowInvocation(ctx, mutatingCapable)
	if borrowErr != nil {
		return "", fmt.Errorf("worker %s checkpoint binding: %w", w.ID, borrowErr)
	}
	defer borrow.Close()
	ctx = borrow.Bind(ctx)
	// Install before registry admission publishes running and before started
	// callbacks can request Stop. Cleanup remains inside runMu on every exit.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w.setCancel(cancel)
	defer w.clearCancel()
	if workers != nil {
		if err := workers.startContinuation(w); err != nil {
			return "", err
		}
	}
	w.setState(func(w *Worker) {
		w.Status = "running"
		w.UpdatedAt = time.Now()
		w.LastError = ""
		w.LastResult = ""
		w.lastEvidence = ""
		w.uiActivity = nil
		w.ToolNames = nil
		w.Runs++
		w.activity = WorkerActivity{}
		w.liveRunStarted(time.Now())
	})
	// Save "running" now (the record said "created", runs=0 for the whole
	// run before): a restart then reconciles it as interrupted, and the GUI
	// reads a definite state even without live progress frames.
	w.registry.persist(w)
	emit := workerProgressSink(ctx, w, w.Runs)
	defer func() {
		s := w.Snapshot()
		emit(WorkerProgressEvent{Kind: "finished", Status: s.Status, Err: s.LastError})
	}()
	defer func() {
		if p := recover(); p != nil {
			w.setState(func(w *Worker) {
				w.Status, w.LastError, w.UpdatedAt = "failed", fmt.Sprintf("worker panicked: %v", p), time.Now()
				w.liveRunEnded()
			})
			panic(p)
		}
	}()
	emit(WorkerProgressEvent{Kind: "started", Prompt: prompt, Status: "running"})

	if w.Loop == nil {
		w.setState(func(w *Worker) {
			w.Status = "failed"
			w.LastError = "worker loop is nil"
			w.liveRunEnded()
		})
		return "", fmt.Errorf("worker %s: loop is nil", w.ID)
	}
	invocation, _ := ctx.Value(workerInvocationKey{}).(workerInvocation)
	w.Loop.verificationObserver = invocation.checks
	events, err := w.Loop.Run(runCtx, prompt)
	if err != nil {
		stopped := w.clearCancel()
		if stopped {
			err = fmt.Errorf("worker %s stopped by request: %w", w.ID, err)
		}
		w.setState(func(w *Worker) {
			w.Status = "failed"
			if stopped {
				w.Status = "stopped"
			}
			w.UpdatedAt = time.Now()
			w.LastError = err.Error()
			w.liveRunEnded()
		})
		return "", err
	}

	var text strings.Builder
	var runErr error
	var rejectedSteering []string
	toolCalls := make(map[string]ToolCallEvent)
	var evidence workerEvidenceLog
	ui := &workerUIActivity{Version: 1, ID: w.ID, Run: w.Snapshot().Runs, ParentCallID: invocation.callID, Calls: []workerUIToolCall{}}
	// The journal is shared with LiveActivity readers: mutate it only under stateMu.
	w.setState(func(w *Worker) { w.liveUI = ui })
	defer func() {
		w.setState(func(w *Worker) {
			w.uiActivity = ui.encode()
			w.liveRunEnded()
		})
	}()
	defer func() { w.setState(func(w *Worker) { w.lastEvidence = evidence.text() }) }()
	for ev := range events {
		now := time.Now()
		w.setState(func(w *Worker) { w.liveObserve(ev, now) })
		switch e := ev.(type) {
		case MessageEvent:
			text.WriteString(e.Text)
		case ToolCallEvent:
			handle, partial := retainWorkerToolDetail(runCtx, w.Loop.toolOutputs, e, ToolResultEvent{})
			w.setState(func(*Worker) { ui.call(e, handle) })
			// Commentary before a tool call is not the worker's final report.
			// Keep only the final assistant turn; reasoning stays in its own loop.
			text.Reset()
			toolCalls[e.ID] = e
			activity := observedWorkerActivity("tool_call", e.ID, e)
			w.setState(func(w *Worker) {
				w.activity = activity
				if len(w.ToolNames) < 32 {
					w.ToolNames = append(w.ToolNames, e.Name)
				}
			})
			emit(WorkerProgressEvent{
				Kind: "tool_call", CallID: e.ID, Tool: e.Name,
				Args: toolcore.HeadTail(e.Args, 700, 240), DetailHandle: handle, Partial: partial,
			})
		case ToolResultEvent:
			handle, partial := retainWorkerToolDetail(runCtx, w.Loop.toolOutputs, toolCalls[e.ID], e)
			w.setState(func(*Worker) { ui.result(e, handle) })
			activity := observedWorkerActivity("tool_result", e.ID, toolCalls[e.ID])
			w.setState(func(w *Worker) { w.activity = activity })
			evidence.add(toolCalls[e.ID], e)
			progress := WorkerProgressEvent{
				Kind: "tool_result", CallID: e.ID, Tool: toolCalls[e.ID].Name,
				Output: toolcore.HeadTail(e.Output, 220, 80), DetailHandle: handle, Partial: partial,
			}
			if e.Err != nil {
				progress.Err = toolcore.HeadTail(e.Err.Error(), 180, 60)
			}
			for _, img := range e.Images {
				if img.Path != "" {
					progress.Images = append(progress.Images, img.Path)
				}
			}
			emit(progress)
			delete(toolCalls, e.ID)
		case ToolArgsProgressEvent:
			// The model is writing a call (e.g. a large file): UI-only.
			emit(WorkerProgressEvent{Kind: "tool_args", Tool: e.Tool, Bytes: e.Bytes})
		case ToolProgressEvent:
			// Live progress of a long tool (e.g. the last output line of a
			// build): UI-only, never part of the report or evidence.
			if e.Text != "" {
				emit(WorkerProgressEvent{Kind: "tool_progress", CallID: e.ID, Tool: toolCalls[e.ID].Name, Output: toolcore.HeadTail(e.Text, 160, 0)})
			}
		case DoneEvent:
			w.setState(func(w *Worker) {
				w.TokensIn += e.Usage.Input
				w.TokensOut += e.Usage.Output
				if e.Steps > 0 {
					w.Steps += e.Steps
				} else {
					w.Steps++
				}
			})
		case steeringDeliveryEvent:
			progress := WorkerProgressEvent{Kind: "steering_delivered", CallID: e.ID, Prompt: toolcore.HeadTail(e.Text, 180, 60), Status: "running"}
			if e.Err != nil {
				progress.Kind = "steering_rejected"
				progress.Err = e.Err.Error()
				rejectedSteering = append(rejectedSteering, fmt.Sprintf("%s rejected: %s", e.ID, e.Err))
			} else {
				// The final report belongs to the latest instruction, not the
				// completed answer that the coordinator just corrected.
				text.Reset()
			}
			emit(progress)
		case ErrorEvent:
			// Drain through channel close: Run still owns its conversation and
			// may emit rejected steering receipts during bounded final cleanup.
			if runErr == nil {
				runErr = e.Err
				w.setState(func(w *Worker) {
					w.TokensIn += e.Usage.Input
					w.TokensOut += e.Usage.Output
					w.Steps += e.Steps
				})
			}
		}
	}
	stopped := w.clearCancel()
	result := strings.TrimSpace(stripThinking(text.String()))
	if len(rejectedSteering) > 0 {
		result += "\n[Steering delivery: " + strings.Join(rejectedSteering, "; ") + "]"
	}
	if runErr != nil || stopped {
		if stopped {
			runErr = fmt.Errorf("worker %s stopped by request", w.ID)
		}
		w.setState(func(w *Worker) {
			w.Status = "failed"
			if stopped {
				w.Status = "stopped"
			}
			w.UpdatedAt = time.Now()
			w.LastResult = result
			w.LastError = runErr.Error()
			if len(rejectedSteering) > 0 {
				w.LastError += "; " + strings.Join(rejectedSteering, "; ")
			}
		})
		return result, runErr
	}
	// The streamed report also lives in canonical history. Reuse its immutable
	// bytes when exactly equal instead of retaining a second completed report.
	result = canonicalWorkerReport(w.Loop, result)
	w.setState(func(w *Worker) {
		w.Status = "done"
		w.UpdatedAt = time.Now()
		w.LastResult = result
	})
	return result, nil
}

func renderWorkerNotification(w *Worker, result string) string {
	s := w.Snapshot()
	return renderWorkerNotificationFromSnapshot(s, workerSummaryFromSnapshot(s), result)
}

func renderWorkerNotificationFromSnapshot(s Snapshot, summary, result string) string {
	status := s.Status
	if status == "" {
		status = "done"
	}
	toolsUsed := workerToolSummary(s.ToolNames)
	metadata := ""
	if s.WorkerModel != "" {
		metadata = fmt.Sprintf("\n<worker-model>%s</worker-model>\n<reasoning-effort>%s</reasoning-effort>", s.WorkerModel, s.ReasoningEffort)
	}
	review := ""
	if r := strings.TrimSpace(s.Review); r != "" {
		// One line, no markup: hosts parse the envelope strictly.
		r = strings.NewReplacer("<", "(", ">", ")", "\r", " ", "\n", " ").Replace(r)
		review = "\n<review>" + r + "</review>"
	}
	return fmt.Sprintf(`<task-notification>
<task-id>%s</task-id>
<agent>%s</agent>%s
<status>%s</status>%s
<summary>%s</summary>
<tools>%s</tools>
<result>%s</result>
</task-notification>`, s.ID, s.Agent, metadata, status, review, summary, toolsUsed, result)
}

func workerToolSummary(names []string) string {
	if len(names) == 0 {
		return ""
	}
	counts := make(map[string]int, len(names))
	order := make([]string, 0, len(names))
	for _, name := range names {
		if counts[name] == 0 {
			order = append(order, name)
		}
		counts[name]++
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		if counts[name] == 1 {
			parts = append(parts, name)
		} else {
			parts = append(parts, fmt.Sprintf("%s×%d", name, counts[name]))
		}
	}
	return strings.Join(parts, ", ")
}

func workerSummary(w *Worker) string {
	if w == nil {
		return "worker unknown"
	}
	s := w.Snapshot()
	return workerSummaryFromSnapshot(s)
}

func workerSummaryFromSnapshot(s Snapshot) string {
	status := s.Status
	if status == "" {
		status = "done"
	}
	// One-line status the coordinator can relay: kind, outcome, and the
	// resource cost (steps + tokens) so a run that hit a limit is legible.
	summary := fmt.Sprintf("%s %s · %d steps · %d in/%d out tok",
		s.Agent, status, s.Steps, s.TokensIn, s.TokensOut)
	// model-per-task telemetry: name the backend only when it differs
	// from the coordinator's (Model is set by the task tool then), so
	// the default single-model line keeps its historical format.
	if s.Model != "" {
		summary += " · model=" + s.Model
	}
	if s.LastError != "" {
		summary += ": " + s.LastError
		if strings.Contains(strings.ToLower(s.LastError), "max steps") {
			summary += fmt.Sprintf("; continue this worker with send_message to %s instead of starting over", s.ID)
		}
	}
	return summary
}
