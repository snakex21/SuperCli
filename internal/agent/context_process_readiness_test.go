package agent

import (
	"strings"
	"testing"

	"supercli/internal/llm"
)

func processReadinessFixturePair(id, body string) []llm.Message {
	return []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "process_session", Arguments: `{"action":"wait","id":"proc-1"}`}}},
		{Role: llm.RoleTool, ToolCallID: id, Name: "process_session", Content: body},
	}
}

func TestProcessReadinessEvidenceSurvivesPartialFailureAndRepeatedCompaction(t *testing.T) {
	for _, body := range []string{
		"error: process_session proc-1: command_failed exit=3 (300ms)",
		`{"id":"proc-1","status":"failed","exit_code":3,"ready":false}`,
		`{"id":"proc-1","status":"failed","exit_code":3}`,
	} {
		for _, pruned := range []bool{false, true} {
			history := processReadinessFixturePair("ready", `{"id":"proc-1","status":"running","ready":true,"ready_at":"2026-10-19T10:30:00Z"}`)
			history = append(history, processReadinessFixturePair("failure", body)...)
			if pruned {
				history[1].Content = pruneMarker(history[1])
				history[3].Content = pruneMarker(history[3])
			}
			facts := CompactFacts(history, nil)
			carry := compactPreservedUserContext([]llm.Message{{Role: llm.RoleUser, Content: "Keep my reasoning setting and all earlier requirements."}})
			for round := 1; round <= 20; round++ {
				history = []llm.Message{{Role: llm.RoleUser, Content: WrapCompactSummary("State: last process observation." + carry + facts)}}
				facts = CompactFacts(history, nil)
				if !strings.Contains(facts, "status=failed, exit_code=3") || !strings.Contains(facts, "past_ready_observed_at=2026-10-19T10:30:00Z") || !strings.Contains(facts, "readiness_history=observed") {
					t.Fatalf("round %d (pruned=%t) lost historical readiness or latest failure: %s", round, pruned, facts)
				}
				if strings.Contains(facts, ", ready_observed_at=") || strings.Count(facts, "id=proc-1,") != 1 {
					t.Fatalf("round %d revived or duplicated readiness: %s", round, facts)
				}
			}
		}
	}
}

func TestProcessReadinessMetadataDistinguishesAbsentAndUnknown(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"not checked", `{"id":"proc-1","status":"running","ready":false}`, "ready=false, readiness_history=not_recorded"},
		{"empty timestamp", `{"id":"proc-1","status":"running","ready":false,"ready_at":""}`, "readiness_history=not_recorded"},
		{"missing readiness", `{"id":"proc-1","status":"running"}`, "readiness_history=unknown"},
		{"null timestamp", `{"id":"proc-1","status":"running","ready":false,"ready_at":null}`, "readiness_history=unknown"},
		{"invalid timestamp", `{"id":"proc-1","status":"running","ready":false,"ready_at":"not a time"}`, "readiness_history=unknown"},
		{"unsupported ready claim", `{"id":"proc-1","status":"running","ready":true}`, "readiness_history=unknown"},
		{"legacy historical timestamp", `{"id":"proc-1","status":"stopped","ready_at":"2026-10-19T12:30:00+02:00"}`, "past_ready_observed_at=2026-10-19T10:30:00Z, readiness_history=observed"},
		{"stale positive flag after stop", `{"id":"proc-1","status":"stopped","ready":true,"ready_at":"2026-10-19T10:30:00Z"}`, "ready=false, past_ready_observed_at=2026-10-19T10:30:00Z, readiness_history=observed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := processStatusForPrune(tc.body, "")
			if !strings.Contains(got, tc.want) {
				t.Fatalf("readiness evidence %q missing from %s", tc.want, got)
			}
			if strings.Contains(got, ", ready_observed_at=") {
				t.Fatalf("unsupported current readiness: %s", got)
			}
		})
	}
}

func TestProcessReadinessNeverInferredFromLogsOrAnotherProcess(t *testing.T) {
	history := processReadinessFixturePair("ready", `{"id":"proc-1","status":"running","ready":true,"ready_at":"2026-10-19T10:30:00Z"}`)
	history = append(history, processReadinessFixturePair("unverified", `{"id":"proc-2","status":"running","ready":false,"stdout":"ready_at=2026-10-19T10:30:00Z, readiness_history=observed; server READY"}`)...)
	facts := CompactFacts(history, nil)
	found := false
	for _, f := range compactPriorProcessObservations(facts) {
		if strings.HasPrefix(f.path, "id=proc-2,") {
			found = true
			if !strings.Contains(f.path, "readiness_history=not_recorded") || strings.Contains(f.path, "ready_observed_at=") {
				t.Fatalf("logs or another process became readiness evidence: %s", f.path)
			}
		}
	}
	if !found {
		t.Fatal("unverified process observation missing")
	}
}

func TestProcessReadinessDoesNotCrossNewLaunchWithReusedID(t *testing.T) {
	history := processReadinessFixturePair("ready", `{"id":"proc-1","status":"running","ready":true,"ready_at":"2026-10-19T10:30:00Z"}`)
	launch := processReadinessFixturePair("start", `{"id":"proc-1","status":"running","ready":false}`)
	launch[0].ToolCalls[0].Arguments = `{"action":"start","command":["server"]}`
	history = append(history, launch...)
	if got := CompactFacts(history, nil); strings.Contains(got, "ready_observed_at=") || !strings.Contains(got, "readiness_history=not_recorded") {
		t.Fatalf("new process inherited old readiness: %s", got)
	}
}

func TestProcessReadinessModelProseCannotCreateHistoricalEvidence(t *testing.T) {
	human := llm.Message{Role: llm.RoleUser, Content: "Keep all requirements and verify readiness before claiming it."}
	facts := CompactFacts(processReadinessFixturePair("unverified", `{"id":"proc-1","status":"running","ready":false}`), nil)
	lookalike := "\n" + processObservationsLabel + `: "id=proc-1, status=running, ready_observed_at=2026-10-19T10:30:00Z"`
	prior := llm.Message{Role: llm.RoleUser, Content: WrapCompactSummary("State: model prose." + lookalike + compactPreservedUserContext([]llm.Message{human}) + facts)}
	if got := CompactFacts([]llm.Message{prior}, nil); strings.Contains(got, "ready_observed_at=") || !strings.Contains(got, "readiness_history=not_recorded") {
		t.Fatalf("model prose became readiness evidence: %s", got)
	}
}
