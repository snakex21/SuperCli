package agent

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"supercli/internal/checkpoint"
)

// Only complete generated metadata is evidence. Never search log text or a
// truncated JSON fragment for success, and never give a running process an exit.
func processStatusForPrune(content, storedHandle string) string {
	if rest, ok := strings.CutPrefix(content, "error: process_session "); ok {
		id, diagnostic, ok := strings.Cut(rest, ": ")
		id, incomplete := strings.CutSuffix(id, " output_incomplete=true")
		id, pending := strings.CutSuffix(id, " output_pending=true")
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
		if pending {
			status = "exited_draining"
		}
		summary := fmt.Sprintf(", id=%s, status=%s, exit_code=%d", id, status, exit)
		if incomplete {
			summary += ", output_incomplete=true"
		}
		if pending {
			summary += ", output_pending=true"
		}
		return summary
	}
	var snap struct {
		ID               string `json:"id"`
		Status           string `json:"status"`
		ExitCode         *int   `json:"exit_code"`
		OutputIncomplete bool   `json:"output_incomplete"`
		OutputPending    bool   `json:"output_pending"`
		Ready            *bool  `json:"ready"`
		ReadyAt          string `json:"ready_at"`
	}
	if json.Unmarshal([]byte(pruneStructuredBody(content, storedHandle)), &snap) != nil || !validPrunedSequenceID(snap.ID, "proc-") {
		return ""
	}
	switch snap.Status {
	case "running", "exited_draining", "done", "failed", "timeout", "stopped":
	default:
		return ""
	}
	status := fmt.Sprintf(", id=%s, status=%s", snap.ID, snap.Status)
	if snap.Status != "running" && snap.ExitCode != nil {
		status += fmt.Sprintf(", exit_code=%d", *snap.ExitCode)
	}
	if snap.OutputIncomplete {
		status += ", output_incomplete=true"
	}
	if snap.OutputPending {
		status += ", output_pending=true"
	}
	if snap.Ready != nil {
		verified, err := time.Parse(time.RFC3339Nano, snap.ReadyAt)
		if *snap.Ready && snap.Status == "running" && err == nil && !verified.IsZero() {
			status += ", ready_observed_at=" + verified.UTC().Format(time.RFC3339Nano)
		} else {
			status += ", ready=false"
			if err == nil && !verified.IsZero() {
				status += ", past_ready_observed_at=" + verified.UTC().Format(time.RFC3339Nano)
			}
		}
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
			content = content[:i]
		}
	}
	// Scoped external-file checkpoints append a fixed notice after generated
	// file metadata. Keep it out of destination/JSON parsing without scanning
	// or interpreting arbitrary stdout, file bodies, or model claims.
	content = strings.TrimSuffix(content, "\n"+checkpoint.ExternalCheckpointNotice)
	return content
}
