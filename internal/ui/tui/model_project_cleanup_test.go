package tui

import (
	"context"
	"testing"
)

func TestProjectCleanupCannotReplaceActiveRunOrWorker(t *testing.T) {
	for _, worker := range []bool{false, true} {
		for _, args := range []string{"remove project --checkpoints", "checkpoints project --clear", "cleanup-policy always", "use project"} {
			calls := 0
			m := New(Options{NoColor: true, Language: "pl", Commands: map[string]SlashHandler{"projects": func(context.Context, string) (string, error) { calls++; return "done", nil }}})
			if worker {
				m.workerViews = []workerView{{status: "running"}}
			} else {
				m.busy = true
			}
			next, cmd := m.dispatchSlashCommand(SlashCommand{Name: "projects", Args: args})
			if cmd != nil {
				cmd()
			}
			if calls != 0 || !next.(Model).hasActiveTask() {
				t.Fatalf("project command replaced active task: %q worker=%t", args, worker)
			}
		}
	}
}
