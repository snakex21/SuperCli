// Wave 2 memory wiring: the /memory slash command, the global
// memory home resolution, and the end-of-session auto-save hook.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/storage/memory"
	"supercli/internal/tools"
)

// storeOrNil converts a possibly-nil *memory.Store into a clean
// nil interface so `keeper == nil` checks in the tools work
// (a typed nil pointer inside a non-nil interface would not).
func storeOrNil(s *memory.Store) tools.MemoryKeeper {
	if s == nil {
		return nil
	}
	return s
}

// memoryCommand implements /memory:
//
//	/memory             — overview (recent entries, DB sizes, embeddings)
//	/memory search <q>  — hybrid search across project + global stores
//	/memory forget <id> — delete an entry from whichever store has it
func memoryCommand(ctx context.Context, project, global *memory.Store, briefing, args string) (string, error) {
	if project == nil && global == nil {
		return "memory: no store available (open failed at startup — see logs)", nil
	}
	args = strings.TrimSpace(args)
	switch {
	case args == "":
		return memoryOverview(project, global, briefing), nil
	case strings.HasPrefix(args, "search "):
		q := strings.TrimSpace(strings.TrimPrefix(args, "search "))
		if q == "" {
			return "usage: /memory search <query>", nil
		}
		return memorySearch(ctx, project, global, q), nil
	case strings.HasPrefix(args, "forget "):
		id := strings.TrimSpace(strings.TrimPrefix(args, "forget "))
		if id == "" {
			return "usage: /memory forget <id>", nil
		}
		return memoryForget(project, global, id), nil
	default:
		return "usage: /memory | /memory search <query> | /memory forget <id>", nil
	}
}

func memoryOverview(project, global *memory.Store, briefing string) string {
	var b strings.Builder
	b.WriteString("Persistent memory\n")
	if briefing != "" {
		fmt.Fprintf(&b, "briefing: %d tokens, injected: yes\n", memory.EstimateTokens(briefing))
	} else {
		b.WriteString("briefing: 0 tokens, injected: no (nothing to inject yet)\n")
	}
	writeStore := func(label string, s *memory.Store) {
		if s == nil {
			fmt.Fprintf(&b, "\n%s: unavailable\n", label)
			return
		}
		dbPath := filepath.Join(s.Root(), "memory.db")
		size := "?"
		if fi, err := os.Stat(dbPath); err == nil {
			size = fmt.Sprintf("%.1f KB", float64(fi.Size())/1024)
		}
		emb := s.EmbedderName()
		if emb == "" {
			emb = "off (FTS5 only)"
		}
		fmt.Fprintf(&b, "\n%s: %s (%s, embeddings: %s)\n", label, dbPath, size, emb)
		entries, err := s.Recent("", 5)
		if err != nil || len(entries) == 0 {
			b.WriteString("  (no entries)\n")
			return
		}
		for _, e := range entries {
			content := strings.ReplaceAll(strings.TrimSpace(e.Content), "\n", " ")
			if len(content) > 90 {
				content = content[:90] + "…"
			}
			fmt.Fprintf(&b, "  [%s] %s %s: %s\n", e.ID, e.UpdatedAt.Format("2006-01-02"), e.Scope, content)
		}
	}
	writeStore("project", project)
	writeStore("global", global)
	if global != nil {
		if cards, err := global.ListProjectCards(0); err == nil && len(cards) > 0 {
			fmt.Fprintf(&b, "\nknown projects: %d\n", len(cards))
		}
	}
	b.WriteString("\n/memory search <query> · /memory forget <id>\n")
	return b.String()
}

func memorySearch(ctx context.Context, project, global *memory.Store, q string) string {
	var b strings.Builder
	total := 0
	search := func(label string, s *memory.Store) {
		if s == nil {
			return
		}
		entries, err := s.HybridSearch(ctx, q, 5)
		if err != nil || len(entries) == 0 {
			return
		}
		for _, e := range entries {
			content := strings.ReplaceAll(strings.TrimSpace(e.Content), "\n", " ")
			if len(content) > 100 {
				content = content[:100] + "…"
			}
			fmt.Fprintf(&b, "[%s] %s/%s %s: %s\n", e.ID, label, e.Scope, e.UpdatedAt.Format("2006-01-02"), content)
			total++
		}
	}
	search("project", project)
	search("global", global)
	if total == 0 {
		return fmt.Sprintf("memory: nothing matches %q", q)
	}
	return fmt.Sprintf("%d result(s) for %q:\n%s", total, q, b.String())
}

func memoryForget(project, global *memory.Store, id string) string {
	for _, s := range []*memory.Store{project, global} {
		if s == nil {
			continue
		}
		if _, err := s.Get(id); err == nil {
			if err := s.Delete(id); err != nil {
				return fmt.Sprintf("memory forget: %v", err)
			}
			return fmt.Sprintf("forgot [%s]", id)
		}
	}
	return fmt.Sprintf("memory forget: no entry with id %q", id)
}

// memProgress tracks how much of the conversation the incremental
// background saver has already summarized into task-log entries.
// The mutex also serializes the background saver against the
// end-of-session finalizer so no fragment is summarized twice.
