package agent

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Only complete generated metadata is evidence. Never search log text or a
// truncated JSON fragment for success, and never give a running process an exit.
func processStatusForPrune(content, storedHandle string) string {
	if rest, ok := strings.CutPrefix(content, "error: process_session "); ok {
		id, diagnostic, ok := strings.Cut(rest, ": ")
		if !ok || !validPrunedSequenceID(id, "proc-") {
			return ""
		}
		exit, _, ok := commandOutcomeForPrune("error: "+diagnostic, "")
		if !ok {
			return ""
		}
		status := "failed"
		if strings.HasPrefix(diagnostic, "command_failed timeout ") {
			status = "timeout"
		}
		return fmt.Sprintf(", id=%s, status=%s, exit_code=%d", id, status, exit)
	}
	var snap struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		ExitCode *int   `json:"exit_code"`
	}
	if json.Unmarshal([]byte(pruneStructuredBody(content, storedHandle)), &snap) != nil || !validPrunedSequenceID(snap.ID, "proc-") {
		return ""
	}
	switch snap.Status {
	case "running", "done", "failed", "timeout", "stopped":
	default:
		return ""
	}
	status := fmt.Sprintf(", id=%s, status=%s", snap.ID, snap.Status)
	if snap.Status != "running" && snap.ExitCode != nil {
		status += fmt.Sprintf(", exit_code=%d", *snap.ExitCode)
	}
	return status
}

func validPrunedSequenceID(id, prefix string) bool {
	suffix, ok := strings.CutPrefix(id, prefix)
	if !ok || len(suffix) > 20 {
		return false
	}
	number, err := strconv.ParseUint(suffix, 10, 64)
	return err == nil && number > 0 && strconv.FormatUint(number, 10) == suffix
}

// Strip only a validated output-store footer from otherwise complete JSON.
func pruneStructuredBody(content, storedHandle string) string {
	if storedHandle != "" {
		if i := strings.LastIndexByte(content, '\n'); i >= 0 && strings.HasPrefix(content[i+1:], "[stored tool output: ") {
			return content[:i]
		}
	}
	return content
}
