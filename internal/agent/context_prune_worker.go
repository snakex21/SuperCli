package agent

import (
	"fmt"
	"strings"

	"supercli/internal/tools/core"
)

// Keep the historical worker ID/status from owned framing, not from report prose.
// This does not promise that a worker is still retained, resumable or verified.
func workerStatusForPrune(content, storedHandle string) string {
	// Generated metadata precedes the report. A truncated/unusual header simply
	// stays unknown; never scan a large report for a plausible worker identifier.
	if len(content) > 768 {
		content = content[:768]
	}
	if strings.HasPrefix(content, "[large tool output: ") {
		head, body, ok := strings.Cut(content, "\n")
		if !ok || storedHandle == "" || core.StoredOutputHandle(head) != storedHandle {
			return ""
		}
		content = body
	}
	const interrupted = "error: TOOL_OUTCOME_UNKNOWN: interrupted after dispatch; side effects may have occurred. Check current state before retrying ("
	if strings.HasPrefix(content, interrupted) {
		head, body, ok := strings.Cut(content, "\n")
		if !ok || !strings.HasSuffix(head, ")") {
			return ""
		}
		content, ok = strings.CutPrefix(body, "tool output:\n")
		// A generic tail cannot prove where the original begins.
		if !ok {
			return ""
		}
	}
	var id, status string
	if rest, ok := strings.CutPrefix(content, "error: worker "); ok {
		var agent string
		id, rest, ok = strings.Cut(rest, " (")
		if !ok {
			return ""
		}
		agent, rest, ok = strings.Cut(rest, "), status=")
		if !ok || strings.ContainsAny(agent, "<>\r\n()") {
			return ""
		}
		status, _, ok = strings.Cut(rest, "; call failed: ")
		if !ok {
			return ""
		}
	} else {
		rest, ok := strings.CutPrefix(content, "<task-notification>\n<task-id>")
		if !ok {
			return ""
		}
		id, rest, ok = strings.Cut(rest, "</task-id>\n<agent>")
		if !ok {
			return ""
		}
		agent, rest, ok := strings.Cut(rest, "</agent>\n<status>")
		if !ok || strings.ContainsAny(agent, "<>\r\n") {
			return ""
		}
		status, _, ok = strings.Cut(rest, "</status>\n")
		if !ok {
			return ""
		}
	}
	if !validPrunedSequenceID(id, "worker-") {
		return ""
	}
	switch status {
	case "created", "running", "done", "failed", "stopped":
		return fmt.Sprintf(", worker_id=%s, worker_status=%s", id, status)
	default:
		return ""
	}
}
