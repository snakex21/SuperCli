package agent

import (
	"strings"
	"supercli/internal/llm"
	"testing"
)

func TestCompactionKeepsLastProcessObservationNotLaunchPromise(t *testing.T) {
	var history []llm.Message
	add := func(id, body string) {
		history = append(history, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "process_session", Arguments: `{"action":"poll","id":"proc-1"}`}}}, llm.Message{Role: llm.RoleTool, Name: "process_session", ToolCallID: id, Content: body})
	}
	add("start", `{"id":"proc-1","status":"running","ready":false}`)
	add("ready", `{"id":"proc-1","status":"running","ready":true,"ready_at":"2026-10-11T15:00:00Z"}`)
	add("exit", `{"id":"proc-1","status":"failed","exit_code":7,"ready":false,"ready_at":"2026-10-11T15:00:00Z"}`)
	history = append(history, llm.Message{Role: llm.RoleAssistant, Content: "Server is running!"})
	facts := CompactFacts(history, nil)
	if !strings.Contains(facts, "status=failed") || !strings.Contains(facts, "exit_code=7") || !strings.Contains(facts, "historical") || strings.Contains(facts, "status=running") {
		t.Fatalf("stale process state: %s", facts)
	}
	if !strings.Contains(facts, "ready=false, past_ready_observed_at=2026-10-11T15:00:00Z") {
		t.Fatalf("last readiness timestamp lost its historical meaning: %s", facts)
	}
	history[5].Content = pruneMarker(history[5])
	if got := CompactFacts(history, nil); !strings.Contains(got, "exit_code=7") {
		t.Fatalf("pruned exit lost: %s", got)
	}
	if got := CompactFacts(history[:4], nil); !strings.Contains(got, "ready_observed_at=2026-10-11T15:00:00Z") {
		t.Fatalf("readiness lost: %s", got)
	}
	prior := llm.Message{Role: llm.RoleUser, Content: WrapCompactSummary("Goal: continue.\nDone: launched a server." + facts)}
	if got := CompactFacts([]llm.Message{prior}, nil); !strings.Contains(got, "status=failed") || !strings.Contains(got, "exit_code=7") {
		t.Fatalf("second compaction lost process state: %s", got)
	}
	if got := CompactFacts([]llm.Message{{Role: llm.RoleAssistant, Content: "Server started on 8091"}}, nil); got != "" {
		t.Fatalf("assistant statement became evidence: %s", got)
	}
}

func TestCompactionKeepsPendingCaptureFailure(t *testing.T) {
	body := "error: process_session proc-1 output_pending=true: command_failed exit=7 (300ms)"
	got := processStatusForPrune(body, "")
	if !strings.Contains(got, "status=exited_draining, exit_code=7, output_pending=true") {
		t.Fatalf("pending-output failure lost authoritative metadata: %s", got)
	}
}
