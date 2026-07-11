package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"supercli/internal/tools"
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
		case DoneEvent:
			w.setState(func(w *Worker) {
				w.TokensIn += e.Usage.Input
				w.TokensOut += e.Usage.Output
				w.Steps++
			})
		case ErrorEvent:
			stopped := w.clearCancel()
			w.setState(func(w *Worker) {
				w.UpdatedAt = time.Now()
				w.LastResult = text.String()
				if stopped {
					w.Status = "stopped"
					w.LastError = "stopped by request"
					return
				}
				w.Status = "failed"
				w.LastError = e.Err.Error()
			})
			if stopped {
				return text.String(), fmt.Errorf("worker %s stopped by request", w.ID)
			}
			return text.String(), e.Err
		}
	}
	w.clearCancel()
	w.setState(func(w *Worker) {
		w.Status = "done"
		w.UpdatedAt = time.Now()
		w.LastResult = text.String()
	})
	return text.String(), nil
}

func renderWorkerNotification(w *Worker, result string) string {
	s := w.Snapshot()
	status := s.Status
	if status == "" {
		status = "done"
	}
	summary := workerSummary(w)
	return fmt.Sprintf(`<task-notification>
<task-id>%s</task-id>
<agent>%s</agent>
<status>%s</status>
<summary>%s</summary>
<result>%s</result>
</task-notification>`, s.ID, s.Agent, status, summary, result)
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
	}
	return summary
}
