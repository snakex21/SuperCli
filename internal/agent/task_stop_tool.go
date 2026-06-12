package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"supercli/internal/tools"
)

// TaskStopTool lets the coordinator (or the user via /workers stop)
// cancel a running worker by id. Stopping cancels the worker's run
// context; the worker's own loop reports the cancellation and the
// registry records status "stopped".
type TaskStopTool struct {
	Workers *WorkerRegistry
}

func NewTaskStopTool(workers *WorkerRegistry) *TaskStopTool {
	if workers == nil {
		workers = NewWorkerRegistry()
	}
	return &TaskStopTool{Workers: workers}
}

func (t *TaskStopTool) Spec() tools.Tool {
	return tools.Tool{
		Name:        "task_stop",
		Description: "Stop a running task worker by ID. Use when a worker is stuck, no longer needed, or working on something the user cancelled.",
		Schema: `{
			"type":"object",
			"required":["task_id"],
			"properties":{
				"task_id":{"type":"string","description":"worker id returned by task, e.g. worker-1"}
			}
		}`,
		Fn: t.execute,
	}
}

type taskStopArgs struct {
	TaskID string `json:"task_id"`
}

func (t *TaskStopTool) execute(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
	var args taskStopArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return tools.Result{Err: fmt.Errorf("task_stop: bad args: %w", err)}, nil
	}
	args.TaskID = strings.TrimSpace(args.TaskID)
	if args.TaskID == "" {
		return tools.Result{Err: fmt.Errorf("task_stop: task_id is required")}, nil
	}
	if err := t.Workers.Stop(args.TaskID); err != nil {
		return tools.Result{Err: fmt.Errorf("task_stop: %w", err)}, nil
	}
	return tools.Result{Text: fmt.Sprintf("worker %s stop requested", args.TaskID)}, nil
}
