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
}

func NewSendMessageTool(workers *WorkerRegistry) *SendMessageTool {
	if workers == nil {
		workers = NewWorkerRegistry()
	}
	return &SendMessageTool{Workers: workers}
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
	handoff, err := runWorkerInvocation(llm.WithCallReason(ctx, llm.ReasonResume), w, args.Message, s.Workers)
	return handoff.result(w, err), nil
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
	if !w.runMu.TryLock() {
		return prepareWorkerHandoff(w, ""), fmt.Errorf("worker %s is already running; its current task must finish before a follow-up", w.ID)
	}
	defer w.runMu.Unlock()
	text, err := runWorkerLoopLocked(ctx, w, prompt, workers)
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
	})
	emit := workerProgressSink(ctx, w, w.Runs)
	defer func() {
		s := w.Snapshot()
		emit(WorkerProgressEvent{Kind: "finished", Status: s.Status, Err: s.LastError})
	}()
	defer func() {
		if p := recover(); p != nil {
			w.setState(func(w *Worker) {
				w.Status, w.LastError, w.UpdatedAt = "failed", fmt.Sprintf("worker panicked: %v", p), time.Now()
			})
			panic(p)
		}
	}()
	emit(WorkerProgressEvent{Kind: "started", Prompt: prompt, Status: "running"})

	if w.Loop == nil {
		w.setState(func(w *Worker) {
			w.Status = "failed"
			w.LastError = "worker loop is nil"
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
		})
		return "", err
	}

	var text strings.Builder
	var runErr error
	var rejectedSteering []string
	toolCalls := make(map[string]ToolCallEvent)
	var evidence workerEvidenceLog
	ui := workerUIActivity{Version: 1, ID: w.ID, Run: w.Snapshot().Runs, ParentCallID: invocation.callID, Calls: []workerUIToolCall{}}
	defer func() { data := ui.encode(); w.setState(func(w *Worker) { w.uiActivity = data }) }()
	defer func() { w.setState(func(w *Worker) { w.lastEvidence = evidence.text() }) }()
	for ev := range events {
		switch e := ev.(type) {
		case MessageEvent:
			text.WriteString(e.Text)
		case ToolCallEvent:
			handle, partial := retainWorkerToolDetail(runCtx, w.Loop.toolOutputs, e, ToolResultEvent{})
			ui.call(e, handle)
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
			ui.result(e, handle)
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
			emit(progress)
			delete(toolCalls, e.ID)
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
	return fmt.Sprintf(`<task-notification>
<task-id>%s</task-id>
<agent>%s</agent>%s
<status>%s</status>
<summary>%s</summary>
<tools>%s</tools>
<result>%s</result>
</task-notification>`, s.ID, s.Agent, metadata, status, summary, toolsUsed, result)
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
