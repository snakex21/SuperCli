package agent

import (
	"encoding/json"
	"strings"
	"time"
)

const processObservationsLabel = "process_observations (historical, not current health)"

// A later partial error reports the latest status but does not revoke an
// earlier readiness observation. Carry its time only as past evidence, never
// the old ready flag. The caller groups observations by their generated ID.
func mergeCompactProcessReadiness(prior, latest string) string {
	observedAt := func(entry string) string {
		for _, field := range strings.Split(entry, ", ") {
			key, value, _ := strings.Cut(field, "=")
			if key != "ready_observed_at" && key != "past_ready_observed_at" {
				continue
			}
			if stamp, err := time.Parse(time.RFC3339Nano, value); err == nil && !stamp.IsZero() {
				return stamp.UTC().Format(time.RFC3339Nano)
			}
		}
		return ""
	}
	if observedAt(latest) != "" {
		return latest
	}
	stamp := observedAt(prior)
	if stamp == "" {
		return latest
	}
	fields := strings.Split(latest, ", ")
	kept := fields[:0]
	for _, field := range fields {
		if !strings.HasPrefix(field, "readiness_history=") {
			kept = append(kept, field)
		}
	}
	return strings.Join(kept, ", ") + ", past_ready_observed_at=" + stamp + ", readiness_history=observed"
}

func compactPriorProcessObservations(summary string) []compactFileFact {
	// New summaries have a separate host-owned suffix. Never let a model's
	// lookalike line override it or supply historical evidence for a merge.
	// Keep support for older summaries that predate the exact instruction carry.
	if strings.Contains(summary, compactUserContextStart) {
		start := strings.LastIndex(summary, compactUserContextStart)
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
	}
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
