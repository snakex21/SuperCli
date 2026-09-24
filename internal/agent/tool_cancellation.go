package agent

import (
	"fmt"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// cancelledToolResult closes an already-announced call without inventing a
// success or retrying it. The distinction survives session resume and worker
// continuation. Only interrupted calls add this text to the model's context.
func (l *Loop) cancelledToolResult(tc llm.ToolCall, res tools.Result, cause error, started bool, out chan<- Event) toolResult {
	if started {
		res.Err = fmt.Errorf("TOOL_OUTCOME_UNKNOWN: interrupted after dispatch; side effects may have occurred. Check current state before retrying (%w)", cause)
	} else {
		res.Err = fmt.Errorf("TOOL_NOT_STARTED: turn ended before dispatch; tool was not run (%w)", cause)
	}
	out <- ToolResultEvent{ID: tc.ID, Output: res.Text, Err: res.Err}
	return toolResult{
		failed: true,
		followUps: []llm.Message{{
			Role:       llm.RoleTool,
			ToolCallID: tc.ID,
			Name:       tc.Name,
			Content:    l.registry.ModelResultContent(tc.Name, res),
		}},
	}
}
