package agent

import (
	"encoding/json"
	"strings"
)

const processObservationsLabel = "process_observations (historical, not current health)"

func compactPriorProcessObservations(summary string) []compactFileFact {
	var facts []compactFileFact
	for _, line := range strings.Split(summary, "\n") {
		list, ok := strings.CutPrefix(line, processObservationsLabel+": ")
		if !ok {
			continue
		}
		var entries []string
		if json.Unmarshal([]byte("["+list+"]"), &entries) != nil {
			continue
		}
		for _, entry := range entries {
			if status := processObservationFromMarker(pruneMarkerPrefix + ": process_session, " + entry + "; details omitted]"); status != "" {
				facts = append(facts, compactFileFact{path: strings.TrimPrefix(status, ", ")})
			}
		}
	}
	return facts
}

// Reuse the parser for authoritative generated metadata, never log text or an
// assistant's assertion. Keep process observations beside exact file facts.
func compactProcessObservations(body string) []compactFileFact {
	var bodies []json.RawMessage
	if json.Unmarshal([]byte(body), &bodies) != nil {
		bodies = []json.RawMessage{json.RawMessage(body)}
	}
	var out []compactFileFact
	for _, raw := range bodies {
		status := processStatusForPrune(string(raw), "")
		if status == "" {
			status = processObservationFromMarker(string(raw))
		}
		if status == "" {
			continue
		}
		out = append(out, compactFileFact{path: strings.TrimPrefix(status, ", ")})
	}
	return out
}

func processObservationFromMarker(body string) string {
	rest, ok := strings.CutPrefix(body, pruneMarkerPrefix+": process_session, id=")
	if !ok || !strings.HasSuffix(body, "]") {
		return ""
	}
	id, rest, ok := strings.Cut(rest, ", status=")
	if !ok || !validPrunedSequenceID(id, "proc-") {
		return ""
	}
	metadata, footer, ok := strings.Cut(rest, "; ")
	if !ok || (footer != "details omitted]" && !strings.HasPrefix(footer, "read_output {\"handle\":")) {
		return ""
	}
	status, _, _ := strings.Cut(metadata, ",")
	switch status {
	case "running", "exited_draining", "done", "failed", "timeout", "stopped":
	default:
		return ""
	}
	if len(metadata) > 256 || strings.ContainsAny(metadata, "\r\n[]") {
		return ""
	}
	return ", id=" + id + ", status=" + metadata
}
