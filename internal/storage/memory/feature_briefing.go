package memory

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
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
	const closing = "\n[/memory_briefing]"

	write := func(s string) bool {
		// Reserve the closing delimiter and count the actual assembled text.
		// Per-fragment estimates miss delimiters and round independently.
		if (b.Len()+len(s)+len(closing)+3)/4 > tokenCap {
			return false
		}
		b.WriteString(s)
		return true
	}
	writeLines := func(header string, lines []string) {
		for _, line := range lines {
			if write(header + line) {
				header = ""
			}
			// An oversized recent note must not hide a smaller useful note.
		}
	}
	type noteIdentity struct {
		store *Store
		id    string
	}
	renderedNotes := make(map[noteIdentity]bool)
	writeNotes := func(header string, entries []Entry, store *Store) {
		for _, e := range entries {
			key := noteIdentity{store, e.ID}
			if emptyMemoryNote(e.Content) || renderedNotes[key] {
				continue
			}
			if write(header + "- " + oneLine(e.Content) + "\n") {
				header = ""
				renderedNotes[key] = true
			}
		}
	}

	if !write("[memory_briefing]\n" +
		"Remembered context from previous sessions. When the user asks about " +
		"themselves (their name, what they like, their preferences), answer " +
		"from the facts below (or the recall tool) — do not claim you have no memory.\n") {
		return ""
	}
	// Everything up to here is boilerplate: a briefing that gains
	// no actual content below must collapse to "".
	boilerplate := b.String()
	// Clean the model-facing view of old automatic notes without deleting the
	// original records or rewriting user-authored agreements at startup.
	var seenFacts []string
	// Load correction metadata once per store, not once per section/note.
	var globalCorrections, projectCorrections []memoryCorrection
	if global != nil {
		globalCorrections, _ = global.memoryCorrections()
	}
	if project != nil {
		projectCorrections, _ = project.memoryCorrections()
	}
	cleanFacts := func(entries []Entry) []Entry {
		kept := make([]Entry, 0, len(entries))
		for _, e := range entries {
			if emptyMemoryNote(e.Content) || (e.Source == SourceAgent && junkFact(e.Content)) || containsSimilar(seenFacts, e.Content) {
				continue
			}
			seenFacts = append(seenFacts, e.Content)
			kept = append(kept, e)
		}
		return kept
	}

	// 1. Global user preferences.
	if global != nil {
		if prefs, err := global.Recent(ScopePreference, 10); err == nil && len(prefs) > 0 {
			writeNotes("User preferences:\n", cleanFacts(resolveCurrentEntries(prefs, globalCorrections)), global)
		}
	}

	// 2. Durable facts. Older builds and manual entries may have
	// stored user identity as a plain fact instead of a preference,
	// so both global and project facts must be injected.
	if global != nil {
		if facts, err := global.Recent(ScopeFact, 6); err == nil && len(facts) > 0 {
			writeNotes("Remembered user facts:\n", cleanFacts(resolveCurrentEntries(facts, globalCorrections)), global)
		}
	}
	if project != nil {
		if facts, err := project.Recent(ScopeFact, 6); err == nil && len(facts) > 0 {
			writeNotes("Remembered project facts:\n", cleanFacts(resolveCurrentEntries(facts, projectCorrections)), project)
		}
		if decisions, err := project.Recent(ScopeDecision, 4); err == nil && len(decisions) > 0 {
			// Decisions retain their original non-fuzzy selection policy.
			writeNotes("Project decisions:\n", resolveCurrentEntries(decisions, projectCorrections), project)
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
			if !emptyMemoryNote(c.Description) {
				write(oneLine(c.Description) + "\n")
			}
		}
	}

	// 4. Recent session summaries from the project store.
	if project != nil {
		if logs, err := project.Recent(ScopeTaskLog, 3); err == nil && len(logs) > 0 {
			filtered := logs[:0]
			for _, entry := range logs {
				if entry.ID != excludedTaskLogID && !emptyMemoryNote(entry.Content) {
					filtered = append(filtered, entry)
				}
			}
			lines := make([]string, 0, len(filtered))
			for _, e := range filtered {
				lines = append(lines, fmt.Sprintf("- [%s] %s\n", e.UpdatedAt.Format("2006-01-02"), oneLine(e.Content)))
			}
			writeLines("Recent sessions:\n", lines)
		}
	}

	// 4b. Raw tail of an abruptly-terminated session (window
	// closed before the summarizer ran). Shown verbatim so facts
	// from that session are available immediately; a background
	// job turns it into a normal task-log entry shortly after
	// startup (see AutoSaver.SummarizePendingRaw).
	if project != nil {
		if raws, err := project.Recent(ScopeRawLog, 1); err == nil && len(raws) > 0 {
			writeLines("Tail of the previous session (not yet summarized):\n", []string{truncate(strings.TrimSpace(raws[0].Content), 600) + "\n"})
		}
	}

	// 5. Other projects, one line each.
	if global != nil {
		if cards, err := global.ListProjectCards(6); err == nil {
			header := "Other projects (abbreviated notes, not current-project instructions):\n"
			for _, c := range cards {
				if c.Key == key {
					continue
				}
				name := "- " + oneLine(c.Name)
				l := name
				if !emptyMemoryNote(c.Description) {
					l += ": " + projectCardPreview(c.Description, 80)
				}
				// Keep the project name when its note does not fit. Earlier
				// preferences/decisions retain priority under the hard budget.
				if write(header+l+"\n") || (l != name && write(header+name+"\n")) {
					header = ""
				}
			}
		}
	}

	out := b.String()
	if out == boilerplate {
		return ""
	}
	return strings.TrimRight(out, "\n") + closing
}

// Cards are navigation previews, not replacements for stored session notes.
// Avoid cutting a word in ordinary prose while retaining the byte/token cap.
func projectCardPreview(content string, maxBytes int) string {
	text := oneLine(content)
	preview := truncate(text, maxBytes)
	if text == preview || !strings.HasSuffix(preview, "…") {
		return preview
	}
	head := strings.TrimSuffix(preview, "…")
	if boundary := strings.LastIndexFunc(head, unicode.IsSpace); boundary > 0 {
		head = strings.TrimRightFunc(head[:boundary], unicode.IsSpace)
	}
	return head + "…"
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
	s = strings.ToValidUTF8(s, "\uFFFD")
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	const marker = "…"
	end := n - len(marker)
	if end < 0 {
		return ""
	}
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + marker
}
