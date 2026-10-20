package tui

import (
	"strings"
	"supercli/internal/agent"
	"testing"
)

func TestWorkerStaleCompletionEventPath(t *testing.T) {
	m := New(Options{NoColor: true})
	emit := func(e agent.Event) { next, _ := m.handleAgentEvent(e); m = next.(Model) }
	emit(agent.WorkerProgressEvent{TaskID: "worker-1", ParentCallID: "first", Kind: "started", Run: 1})
	emit(agent.WorkerProgressEvent{TaskID: "worker-1", ParentCallID: "second", Kind: "started", Run: 2})
	emit(agent.WorkerProgressEvent{TaskID: "worker-1", ParentCallID: "second", Kind: "tool_call", Run: 2, Tool: "read_lines"})
	for _, e := range []agent.Event{
		agent.WorkerProgressEvent{TaskID: "worker-1", ParentCallID: "first", Kind: "finished", Run: 1, Status: "done"},
		agent.WorkerProgressEvent{TaskID: "worker-1", ParentCallID: "first", Kind: "tool_result", Run: 1, Err: "old error"},
		agent.WorkerProgressEvent{TaskID: "worker-1", ParentCallID: "first", Kind: "started", Run: 1, Prompt: "stale start"},
		agent.WorkerProgressEvent{TaskID: "worker-1", Kind: "finished", Status: "done"},
		agent.WorkerNotificationEvent{TaskID: "worker-1", Run: 1, Status: "done", Summary: "HISTORICAL_REPORT"},
		agent.WorkerNotificationEvent{TaskID: "worker-1", Status: "done", Summary: "LEGACY_REPORT"},
	} {
		emit(e)
		if w := m.workerViews[0]; w.status != "running" || w.activity != "read_lines" {
			t.Errorf("stale %T clobbered live view: %+v", e, w)
		}
	}
	if !strings.Contains(m.completedLines(), "HISTORICAL_REPORT") {
		t.Fatal("report disappeared")
	}
	emit(agent.WorkerProgressEvent{TaskID: "worker-1", ParentCallID: "second", Kind: "tool_call", Tool: "legacy_callback"})
	if m.workerViews[0].activity != "legacy_callback" {
		t.Fatal("run-zero callback rejected")
	}
	emit(agent.WorkerNotificationEvent{TaskID: "worker-1", Run: 2, Status: "done", Summary: "CURRENT_REPORT"})
	if m.workerViews[0].status != "done" {
		t.Fatal("current finish lost")
	}
	// A different root may legitimately reuse worker-1 and ordinal 1.
	emit(agent.WorkerProgressEvent{TaskID: "worker-1", ParentCallID: "new-root", Kind: "started", Run: 1, Prompt: "fresh"})
	if w := m.workerViews[0]; w.status != "running" || w.activity != "fresh" {
		t.Fatalf("new root rejected: %+v", w)
	}
	// Match the existing resumed-transcript reset boundary.
	m.workerViews = nil
	emit(agent.WorkerNotificationEvent{TaskID: "worker-1", Status: "done", Summary: "INITIAL_LEGACY_NOTE"})
	if m.workerViews[0].status != "done" || !strings.Contains(m.completedLines(), "INITIAL_LEGACY_NOTE") {
		t.Fatal("initial legacy note rejected")
	}
	emit(agent.WorkerProgressEvent{TaskID: "worker-1", ParentCallID: "reset-root", Kind: "started", Run: 1})
	if m.workerViews[0].status != "running" {
		t.Fatal("reset root rejected")
	}
}
