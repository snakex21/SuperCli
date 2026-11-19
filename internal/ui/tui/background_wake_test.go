package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/system/uilang"
	"supercli/internal/tools"
)

// An idle TUI receiving BackgroundWakeEvent starts an automatic turn that
// is marked as such; a busy TUI ignores it (the scheduler re-signals later).
func TestTUIBackgroundWakeStartsMarkedAutomaticTurn(t *testing.T) {
	provider, _ := llm.NewEcho("echo")
	loop, err := agent.NewLoop(agent.LoopConfig{Provider: provider, Registry: tools.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	loop.Messages = append(loop.Messages, llm.Message{Role: llm.RoleUser, Content: "delegate"}, llm.Message{Role: llm.RoleAssistant, Content: "started"})
	loop.DeliverWorkerReport(context.Background(), "<task-notification>TUI_LATE_REPORT</task-notification>")
	m := New(Options{Agent: loop, Language: "en"})

	m.busy = true
	if _, cmd := m.startBackgroundWake(agent.BackgroundWakeEvent{Unseen: 1}); cmd != nil {
		t.Fatal("busy TUI started an automatic turn")
	}
	m.busy = false
	next, cmd := m.startBackgroundWake(agent.BackgroundWakeEvent{Unseen: 1})
	if cmd == nil || !next.busy {
		t.Fatal("idle TUI did not start the automatic turn")
	}
	start, ok := cmd().(runStartMsg)
	if !ok || start.err != nil || start.ch == nil {
		t.Fatalf("automatic run start: %+v", start)
	}
	for range start.ch {
	}
	marker := strings.SplitN(uilang.Text("en", "tui.wake.started"), "%", 2)[0]
	if !strings.Contains(ansi.Strip(next.chat.render(next.palette)), marker) {
		t.Fatal("automatic turn is not marked in the transcript")
	}
	if loop.UnseenBackgroundReports() != 0 {
		t.Fatal("automatic turn did not consume the report")
	}
	for _, msg := range loop.Messages {
		if msg.Role == llm.RoleUser && msg.Name != llm.WorkerNotificationName && msg.Content != "delegate" {
			t.Fatalf("automatic turn added a user message: %q", msg.Content)
		}
	}

	// Nothing left: a second wake is quiet, not an error.
	next.busy = false
	_, cmd = next.startBackgroundWake(agent.BackgroundWakeEvent{Unseen: 1})
	if quiet, _ := cmd().(runStartMsg); quiet.err == nil || !quiet.quiet {
		t.Fatalf("empty wake should be quiet: %+v", quiet)
	}
}

func TestTUIHeaderShowsRunningBackgroundAgents(t *testing.T) {
	m := New(Options{Language: "pl"})
	m.width = 100
	updated, _, handled := m.handleBackgroundExternal(agent.BackgroundAgentsEvent{Active: 2})
	if !handled {
		t.Fatal("indicator event not handled")
	}
	want := strings.Replace(uilang.Text("pl", "tui.status.background"), "%d", "2", 1)
	if !strings.Contains(ansi.Strip(updated.renderHeader()), want) {
		t.Fatalf("badge %q missing: %q", want, ansi.Strip(updated.renderHeader()))
	}
	updated, _, _ = updated.handleBackgroundExternal(agent.BackgroundAgentsEvent{Active: 0})
	if strings.Contains(ansi.Strip(updated.renderHeader()), want) {
		t.Fatal("badge stayed after workers finished")
	}
}
