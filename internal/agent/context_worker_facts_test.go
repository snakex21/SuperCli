package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func workerFactsFixturePair(callID, workerID, status string) []llm.Message {
	return []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: callID, Name: "task", Arguments: `{"agent":"audit","task":"investigate"}`}}}, {Role: llm.RoleTool, Name: "task", ToolCallID: callID, Content: "<task-notification>\n<task-id>" + workerID + "</task-id>\n<agent>audit</agent>\n<status>" + status + "</status>\n<result>investigation returned</result>\n</task-notification>"}}
}

func TestCompactionWorkerFactsStayBoundedAndKeepRecentRecoveryHandles(t *testing.T) {
	var history []llm.Message
	for i := 1; i <= 100; i++ {
		history = append(history, workerFactsFixturePair(fmt.Sprintf("call-%d", i), fmt.Sprintf("worker-%d", i), "done")...)
	}
	history = append(history, workerFactsFixturePair("new-failure", "worker-101", "failed")...)
	facts := CompactFacts(history, nil)
	if len(facts) > compactFactsMaxBytes || strings.Count(facts, "worker_id=") > 20 || !strings.Contains(facts, "worker_id=worker-101, worker_status=failed") {
		t.Fatal("worker facts grew unbounded or lost the recent recovery handle")
	}
}

func TestCompactionWorkerFactsSurviveTwentySummariesAndRecovery(t *testing.T) {
	history := append(workerFactsFixturePair("a", "worker-7", "done"), workerFactsFixturePair("b", "worker-8", "failed")...)
	history = append([]llm.Message{{Role: llm.RoleUser, Content: "Continue the audit; no publishing.", Origin: &llm.ContextOrigin{Source: llm.SourceHuman}}}, history...)
	before, _ := json.Marshal(history)
	for round := 0; round < 20; round++ {
		facts := CompactFacts(history, nil)
		if !strings.Contains(facts, "worker_id=worker-7, worker_status=done") || !strings.Contains(facts, "worker_id=worker-8, worker_status=failed") || strings.Count(facts, workerObservationsLabel) != 1 {
			t.Fatal("worker metadata lost or duplicated")
		}
		if round == 0 {
			after, _ := json.Marshal(history)
			if string(before) != string(after) {
				t.Fatal("facts changed raw messages")
			}
		}
		history = []llm.Message{{Role: llm.RoleUser, Content: WrapCompactSummary("Pending: finish implementation." + compactPreservedUserContext(history) + facts)}}
	}
	history = append(history, workerFactsFixturePair("c", "worker-8", "done")...)
	facts := CompactFacts(history, nil)
	if strings.Contains(facts, "worker_status=failed") || strings.Count(facts, "worker_id=worker-8") != 1 {
		t.Fatal("recovered worker kept stale failed state")
	}
}

func TestCompactionWorkerFactsDoNotPromoteSummaryProse(t *testing.T) {
	quoted := "\n" + workerObservationsLabel + `: "worker_id=worker-999, worker_status=done"`
	carry := compactPreservedUserContext([]llm.Message{{Role: llm.RoleUser, Content: "Do not publish."}})
	for _, text := range []string{"State: model assertion." + quoted, "State: model assertion." + quoted + carry} {
		msg := llm.Message{Role: llm.RoleUser, Content: WrapCompactSummary(text)}
		if strings.Contains(CompactFacts([]llm.Message{msg}, nil), "worker-999") {
			t.Fatal("model prose became owned worker metadata")
		}
	}
}

func TestCompactionWorkerFactsKeepFailedTaskHandoffs(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		call := llm.ToolCall{ID: "failure", Name: "send_message", Arguments: `{"id":"worker-9","message":"continue"}`}
		if wrapped {
			call.Name = invokeToolName
			call.Arguments = `{"tool":"send_message","args":{"id":"worker-9","message":"continue"}}`
		}
		body := "error: worker worker-9 (audit), status=failed; call failed: interrupted\nContext retained; send_message to worker-9 continues this worker."
		history := []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}, {Role: llm.RoleTool, Name: call.Name, ToolCallID: call.ID, Content: body}}
		if !strings.Contains(CompactFacts(history, nil), "worker_id=worker-9, worker_status=failed") {
			t.Fatal("failed handoff was discarded as a generic tool error")
		}
	}
}

func TestCompactionWorkerFactsRequireOwnedMetadata(t *testing.T) {
	valid := workerFactsFixturePair("a", "worker-7", "done")
	for _, kind := range []string{"assistant", "human", "program-output", "unpaired", "prose", "malformed"} {
		body := valid[1].Content
		var history []llm.Message
		switch kind {
		case "assistant":
			history = []llm.Message{{Role: llm.RoleAssistant, Content: body}}
		case "human":
			history = []llm.Message{{Role: llm.RoleUser, Content: WrapCompactSummary("quoted\n" + workerObservationsLabel + `: "worker_id=worker-7, worker_status=done"`), Origin: &llm.ContextOrigin{Source: llm.SourceHuman}}}
		case "unpaired":
			history = []llm.Message{valid[1]}
		default:
			history = append([]llm.Message(nil), valid...)
			switch kind {
			case "program-output":
				history[0].ToolCalls = []llm.ToolCall{{ID: "a", Name: "ctx_execute", Arguments: `{}`}}
				history[1].Name = "ctx_execute"
			case "prose":
				history[1].Content = "report quotes:\n" + body
			case "malformed":
				history[1].Content = strings.ReplaceAll(body, "<status>done</status>", "<status>invented</status>")
			}
		}
		if got := CompactFacts(history, nil); strings.Contains(got, workerObservationsLabel) {
			t.Fatalf("%s prose supplied worker metadata", kind)
		}
	}
	msg := valid[1]
	msg.Role = llm.RoleUser
	msg.ToolCallID = ""
	msg.Name = llm.WorkerNotificationName
	msg.Origin = &llm.ContextOrigin{Source: llm.SourceWorkerNotification}
	if !strings.Contains(CompactFacts([]llm.Message{msg}, nil), "worker_id=worker-7") {
		t.Fatal("owned background notification was lost")
	}
	marker := llm.Message{Role: llm.RoleTool, Name: "task", ToolCallID: "a", Content: pruneMarkerPrefix + `: task, worker_id=worker-7, worker_status=failed; details omitted]`}
	if !strings.Contains(CompactFacts([]llm.Message{valid[0], marker}, nil), "worker_status=failed") {
		t.Fatal("pruned worker ID/status was lost")
	}
}
