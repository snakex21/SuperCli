package tui

import (
	"strings"
	"supercli/internal/agent"
	"testing"
)

func TestWorkerSteeringReceiptPreservesRunningStatus(t *testing.T) {
	m := New(Options{NoColor: true, Language: "pl"})
	m.width, m.height = 90, 28
	m.updateWorkerView(agent.WorkerProgressEvent{TaskID: "worker-1", Agent: "code", Kind: "started", Prompt: "original"})
	next, _ := m.handleAgentEvent(agent.WorkerProgressEvent{TaskID: "worker-1", Agent: "code", Kind: "steering_delivered", CallID: "steer-1", Prompt: "Keep the existing evidence"})
	m = next.(Model)
	if m.workerViews[0].status != "running" || !strings.Contains(strings.Join(m.workerPanelLines(), "\n"), "send_message") || !strings.Contains(m.completedLines(), "Keep the existing evidence") {
		t.Fatalf("delivery hid the active worker or receipt: %+v %s", m.workerViews, m.completedLines())
	}
	next, _ = m.handleAgentEvent(agent.WorkerProgressEvent{TaskID: "worker-1", Agent: "code", Kind: "steering_rejected", CallID: "steer-2", Err: "run stopped before delivery"})
	m = next.(Model)
	if m.workerViews[0].status != "running" || !strings.Contains(m.completedLines(), "run stopped before delivery") {
		t.Fatalf("rejection changed run status or was hidden: %+v %s", m.workerViews, m.completedLines())
	}
}
