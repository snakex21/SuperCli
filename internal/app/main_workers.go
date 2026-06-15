package app

import (
	"fmt"
	"strings"
	"time"

	"supercli/internal/agent"
)

// formatWorkers renders the /workers panel: one line per worker with id,
// agent kind, status, age, token usage, and a shortened task description.
func formatWorkers(reg *agent.WorkerRegistry) string {
	workers := reg.List()
	if len(workers) == 0 {
		return "workers: none yet (the coordinator spawns them via the task tool)"
	}
	var b strings.Builder
	b.WriteString("Workers:\n")
	now := time.Now()
	for _, w := range workers {
		s := w.Snapshot()
		age := now.Sub(s.UpdatedAt).Round(time.Second)
		tokens := ""
		if s.TokensIn > 0 || s.TokensOut > 0 {
			tokens = fmt.Sprintf("  %d in / %d out tok", s.TokensIn, s.TokensOut)
		}
		fmt.Fprintf(&b, "  %-10s %-8s %-8s %6s ago%s\n", s.ID, s.Agent, s.Status, age, tokens)
		desc := shortenLine(strings.ReplaceAll(s.Description, "\n", " "), 100)
		if desc != "" {
			fmt.Fprintf(&b, "             %s\n", desc)
		}
		if s.LastError != "" && s.Status != "done" {
			fmt.Fprintf(&b, "             error: %s\n", shortenLine(s.LastError, 100))
		}
	}
	b.WriteString("\nstop a running worker: /workers stop <id>\n")
	return b.String()
}
