package agent

import (
	"encoding/json"
	"strings"
)

const workerObservationsLabel = "worker_observations (historical, not current execution)"

// Carry only owned ID/status metadata, never assertions from a worker's prose.
// Historical completion does not mean the overall task is done or a worker
// remains live/resumable after restart.
func compactWorkerObservation(body string) string {
	status := workerStatusForPrune(body, "")
	if status != "" {
		return strings.TrimPrefix(status, ", ")
	}
	for _, tool := range []string{"task", "send_message"} {
		prefix := pruneMarkerPrefix + ": " + tool + ", "
		if rest, ok := strings.CutPrefix(body, prefix); ok && strings.HasSuffix(body, "]") {
			entry, footer, ok := strings.Cut(rest, "; ")
			if ok && (footer == "details omitted]" || strings.HasPrefix(footer, "read_output {\"handle\":")) && validCompactWorkerObservation(entry) {
				return entry
			}
		}
	}
	return ""
}

func validCompactWorkerObservation(entry string) bool {
	id, rest, ok := strings.Cut(strings.TrimPrefix(entry, "worker_id="), ", worker_status=")
	if !ok || !strings.HasPrefix(entry, "worker_id=") || !validPrunedSequenceID(id, "worker-") {
		return false
	}
	switch rest {
	case "created", "running", "done", "failed", "stopped":
		return true
	}
	return false
}

func compactPriorWorkerObservations(summary string) []compactFileFact {
	// This feature emits facts after the exact instruction carry. Ignore
	// lookalike lines in the model's prose or quoted historical instructions.
	start := strings.LastIndex(summary, compactUserContextStart)
	if start < 0 {
		return nil
	}
	carryText := summary[start+len(compactUserContextStart):]
	end := strings.Index(carryText, compactUserContextEnd)
	if end < 0 {
		return nil
	}
	var carry compactUserContext
	if json.Unmarshal([]byte(carryText[:end]), &carry) != nil || len(carry.Requests)+len(carry.PriorState) == 0 {
		return nil
	}
	summary = carryText[end+len(compactUserContextEnd):]
	var facts []compactFileFact
	for _, line := range strings.Split(summary, "\n") {
		list, ok := strings.CutPrefix(line, workerObservationsLabel+": ")
		if !ok {
			continue
		}
		var entries []string
		if json.Unmarshal([]byte("["+list+"]"), &entries) != nil {
			continue
		}
		for _, entry := range entries {
			if validCompactWorkerObservation(entry) {
				facts = append(facts, compactFileFact{path: entry})
			}
		}
	}
	return facts
}
