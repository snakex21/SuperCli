package memory

import (
	"fmt"
	"strings"
	"time"
)

// Memory entry scopes used by the remember tool's `type` field.
// They double as markdown file names (scope-fact.md etc., see
// ScopeFile) so the on-disk mirror stays browsable per type.
const (
	ScopeFact       = "fact"
	ScopePreference = "preference"
	ScopeDecision   = "decision"
	ScopeTaskLog    = "task-log"
	// ScopeRawLog holds emergency dumps of the un-summarized
	// transcript tail written when the console window is closed
	// (CTRL_CLOSE_EVENT) — no time for an LLM call there. They
	// are summarized into task-log entries at the next startup
	// (AutoSaver.SummarizePendingRaw) and shown raw in the
	// briefing until then.
	ScopeRawLog = "raw-log"
)

// BuildBriefing renders the session-start briefing injected into
// the system prompt by code (not by a model call):
//
//   - global user preferences
//   - this project's card (description, state, last session)
//   - short summaries of the last 2–3 sessions (task-log entries)
//   - one line per other known project
//
// tokenCap bounds the result (~500-800 for normal tiers, less for
// small). Either store may be nil; missing data simply shrinks
// the briefing. An empty string means "nothing worth injecting".
func BuildBriefing(global, project *Store, projectPath string, tokenCap int) string {
	return BuildBriefingExcludingTaskLog(global, project, projectPath, tokenCap, "")
}

// BuildBriefingExcludingTaskLog is BuildBriefing with one task-log entry
// omitted. A live conversation uses this to exclude its own capsule: the full
// transcript is already in model context, and reinjecting its just-saved
// summary would both duplicate context and mutate the cacheable system prefix
// between turns.
func BuildBriefingExcludingTaskLog(global, project *Store, projectPath string, tokenCap int, excludedTaskLogID string) string {
	if tokenCap <= 0 {
		tokenCap = 700
	}
	var b strings.Builder
	budget := tokenCap

	write := func(s string) bool {
		t := estimateTokens(s)
		if t > budget {
			return false
		}
		b.WriteString(s)
		budget -= t
		return true
	}

	write("[memory_briefing]\n")
	write("Remembered context from previous sessions. When the user asks about " +
		"themselves (their name, what they like, their preferences), answer " +
		"from the facts below (or the recall tool) — do not claim you have no memory.\n")
	// Everything up to here is boilerplate: a briefing that gains
	// no actual content below must collapse to "".
	boilerplate := b.String()

	// 1. Global user preferences.
	if global != nil {
		if prefs, err := global.Recent(ScopePreference, 10); err == nil && len(prefs) > 0 {
			write("User preferences:\n")
			for _, e := range prefs {
				if !write("- " + oneLine(e.Content) + "\n") {
					break
				}
			}
		}
	}

	// 2. Durable facts. Older builds and manual entries may have
	// stored user identity as a plain fact instead of a preference,
	// so both global and project facts must be injected.
	if global != nil {
		if facts, err := global.Recent(ScopeFact, 6); err == nil && len(facts) > 0 {
			write("Remembered user facts:\n")
			for _, e := range facts {
				if !write("- " + oneLine(e.Content) + "\n") {
					break
				}
			}
		}
	}
	if project != nil {
		if facts, err := project.Recent(ScopeFact, 6); err == nil && len(facts) > 0 {
			write("Remembered project facts:\n")
			for _, e := range facts {
				if !write("- " + oneLine(e.Content) + "\n") {
					break
				}
			}
		}
		if decisions, err := project.Recent(ScopeDecision, 4); err == nil && len(decisions) > 0 {
			write("Project decisions:\n")
			for _, e := range decisions {
				if !write("- " + oneLine(e.Content) + "\n") {
					break
				}
			}
		}
	}

	// 3. This project's card.
	key := ProjectKey(projectPath)
	if global != nil {
		if c, ok := global.GetProjectCard(key); ok {
			line := fmt.Sprintf("Project: %s (%s)", c.Name, c.Path)
			if c.State != "" {
				line += " — " + c.State
			}
			if !c.LastSession.IsZero() && c.LastSession.Unix() > 0 {
				line += ", last session " + c.LastSession.Format("2006-01-02")
			}
			write(line + "\n")
			if c.Description != "" {
				write(oneLine(c.Description) + "\n")
			}
		}
	}

	// 4. Recent session summaries from the project store.
	if project != nil {
		if logs, err := project.Recent(ScopeTaskLog, 3); err == nil && len(logs) > 0 {
			filtered := logs[:0]
			for _, entry := range logs {
				if entry.ID != excludedTaskLogID {
					filtered = append(filtered, entry)
				}
			}
			if len(filtered) > 0 {
				write("Recent sessions:\n")
			}
			for _, e := range filtered {
				line := fmt.Sprintf("- [%s] %s\n", e.UpdatedAt.Format("2006-01-02"), oneLine(e.Content))
				if !write(line) {
					break
				}
			}
		}
	}

	// 4b. Raw tail of an abruptly-terminated session (window
	// closed before the summarizer ran). Shown verbatim so facts
	// from that session are available immediately; a background
	// job turns it into a normal task-log entry shortly after
	// startup (see AutoSaver.SummarizePendingRaw).
	if project != nil {
		if raws, err := project.Recent(ScopeRawLog, 1); err == nil && len(raws) > 0 {
			write("Tail of the previous session (not yet summarized):\n")
			write(truncate(strings.TrimSpace(raws[0].Content), 600) + "\n")
		}
	}

	// 5. Other projects, one line each.
	if global != nil {
		if cards, err := global.ListProjectCards(6); err == nil {
			var lines []string
			for _, c := range cards {
				if c.Key == key {
					continue
				}
				l := "- " + c.Name
				if c.Description != "" {
					l += ": " + truncate(oneLine(c.Description), 80)
				}
				lines = append(lines, l+"\n")
			}
			if len(lines) > 0 && write("Other projects:\n") {
				for _, l := range lines {
					if !write(l) {
						break
					}
				}
			}
		}
	}

	out := b.String()
	if out == boilerplate {
		return ""
	}
	return strings.TrimRight(out, "\n") + "\n[/memory_briefing]"
}

// RefreshCard updates the global store's card for a project with
// a fresh last-session stamp and (optionally) a new description.
func RefreshCard(global *Store, projectPath, description, state string) {
	if global == nil || projectPath == "" {
		return
	}
	_ = global.UpsertProjectCard(ProjectCard{
		Key:         ProjectKey(projectPath),
		Name:        projectName(projectPath),
		Path:        projectPath,
		Description: description,
		State:       state,
		LastSession: time.Now(),
	})
}

func projectName(path string) string {
	path = strings.TrimRight(strings.ReplaceAll(path, "\\", "/"), "/")
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
