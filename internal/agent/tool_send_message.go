package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
		Description: "Continue an existing task worker by ID. Use when the worker's existing context is useful, especially for corrections after failures or continuing research into implementation.",
		Schema: `{
			"type":"object",
			"required":["to","message"],
			"properties":{
				"to":{"type":"string","description":"worker id returned by task, e.g. worker-1"},
				"message":{"type":"string","description":"self-contained follow-up instruction for that worker"}
			}
		}`,
		Fn: s.execute,
	}
}

type sendMessageArgs struct {
	To      string `json:"to"`
	Message string `json:"message"`
}

func (s *SendMessageTool) execute(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
	var args sendMessageArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return tools.Result{Err: fmt.Errorf("send_message: bad args: %w", err)}, nil
	}
	args.To = strings.TrimSpace(args.To)
	args.Message = strings.TrimSpace(args.Message)
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

	text, err := runWorkerLoop(ctx, w, args.Message)
	if err != nil {
		return tools.Result{Text: renderWorkerNotification(w, text), Err: err}, nil
	}
	return tools.Result{Text: renderWorkerNotification(w, text)}, nil
}

func runWorkerLoop(ctx context.Context, w *Worker, prompt string) (string, error) {
	w.runMu.Lock()
	defer w.runMu.Unlock()
	w.setState(func(w *Worker) {
		w.Status = "running"
		w.UpdatedAt = time.Now()
		w.LastError = ""
	})

	if w.Loop == nil {
		w.setState(func(w *Worker) {
			w.Status = "failed"
			w.LastError = "worker loop is nil"
		})
		return "", fmt.Errorf("worker %s: loop is nil", w.ID)
	}
	// Make this run stoppable: task_stop / "/workers stop <id>" cancel
	// the context mid-run. clearCancel tells us whether a failure was an
	// explicit stop (status "stopped") or a real error.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w.setCancel(cancel)

	events, err := w.Loop.Run(runCtx, prompt)
	if err != nil {
		w.clearCancel()
		w.setState(func(w *Worker) {
			w.Status = "failed"
			w.LastError = err.Error()
		})
		return "", err
	}

	var text strings.Builder
	for ev := range events {
		switch e := ev.(type) {
		case MessageEvent:
			text.WriteString(e.Text)
		case ReasoningEvent:
			text.WriteString("<thinking>")
			text.WriteString(e.Text)
			text.WriteString("</thinking>\n")
		case ToolCallEvent:
			w.setState(func(w *Worker) {
				if len(w.ToolNames) < 32 {
					w.ToolNames = append(w.ToolNames, e.Name)
				}
			})
			w.emitProgress(WorkerProgressEvent{
				Kind: "tool_call", CallID: e.ID, Tool: e.Name,
				Args: toolcore.HeadTail(e.Args, 180, 60),
			})
		case ToolResultEvent:
			progress := WorkerProgressEvent{
				Kind: "tool_result", CallID: e.ID,
				Output: toolcore.HeadTail(e.Output, 220, 80),
			}
			if e.Err != nil {
				progress.Err = toolcore.HeadTail(e.Err.Error(), 180, 60)
			}
			w.emitProgress(progress)
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
		case ErrorEvent:
			stopped := w.clearCancel()
			result := strings.TrimSpace(stripThinking(text.String()))
			w.setState(func(w *Worker) {
				w.UpdatedAt = time.Now()
				w.LastResult = result
				w.TokensIn += e.Usage.Input
				w.TokensOut += e.Usage.Output
				w.Steps += e.Steps
				if stopped {
					w.Status = "stopped"
					w.LastError = "stopped by request"
					return
				}
				w.Status = "failed"
				w.LastError = e.Err.Error()
			})
			if stopped {
				return result, fmt.Errorf("worker %s stopped by request", w.ID)
			}
			return result, e.Err
		}
	}
	w.clearCancel()
	result := strings.TrimSpace(stripThinking(text.String()))
	w.setState(func(w *Worker) {
		w.Status = "done"
		w.UpdatedAt = time.Now()
		w.LastResult = result
	})
	return result, nil
}

func renderWorkerNotification(w *Worker, result string) string {
	s := w.Snapshot()
	status := s.Status
	if status == "" {
		status = "done"
	}
	summary := workerSummary(w)
	toolsUsed := workerToolSummary(s.ToolNames)
	return fmt.Sprintf(`<task-notification>
<task-id>%s</task-id>
<agent>%s</agent>
<status>%s</status>
<summary>%s</summary>
<tools>%s</tools>
<result>%s</result>
</task-notification>`, s.ID, s.Agent, status, summary, toolsUsed, result)
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
