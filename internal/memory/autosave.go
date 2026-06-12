package memory

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// AutoSaver is the code-level guarantee behind the "remember
// after each task" instruction in the system prompt: if the model
// finished a session without saving anything, the saver generates
// a one-call summary itself and writes it as a task-log entry,
// then refreshes the project's card in the global store.
type AutoSaver struct {
	Project     *Store
	Global      *Store
	ProjectPath string

	rememberCalls atomic.Int64
}

// NoteRemember is called by the remember tool every time the model
// saves something. A session with at least one save skips the
// synthetic summary (the model already did its job).
func (a *AutoSaver) NoteRemember() {
	if a == nil {
		return
	}
	a.rememberCalls.Add(1)
}

// Remembered reports whether the model called remember at least
// once this session (synthetic summaries are skipped then).
func (a *AutoSaver) Remembered() bool {
	return a != nil && a.rememberCalls.Load() > 0
}

// SummarizeFunc produces a short summary for the given prompt with
// a single LLM call. Wire it to the active provider.
type SummarizeFunc func(ctx context.Context, prompt string) (string, error)

// Finalize runs at session end (and may be called every N
// messages — it is idempotent per call site). transcript is a
// plain-text tail of the conversation; empty transcripts are
// skipped. The summarize call is bounded by the passed context.
func (a *AutoSaver) Finalize(ctx context.Context, transcript string, summarize SummarizeFunc) {
	if a == nil {
		return
	}
	// Always bump the card's last-session stamp.
	defer RefreshCard(a.Global, a.ProjectPath, "", "active")

	if a.rememberCalls.Load() > 0 {
		return // the model saved its own notes this session
	}
	a.StoreSummary(ctx, transcript, summarize)
}

// StoreSummary summarizes one transcript fragment with a single
// LLM call and stores the result as a task-log entry. It is the
// shared engine behind Finalize (session end) and the incremental
// background saver (after each agent turn). Returns true when the
// fragment is "consumed" — stored, judged NOTHING, or empty — and
// false when the summarize call failed (caller may retry with the
// same fragment later).
func (a *AutoSaver) StoreSummary(ctx context.Context, transcript string, summarize SummarizeFunc) bool {
	if a == nil {
		return true
	}
	// Never feed (or store) raw model reasoning: providers wrap
	// reasoning streams in <thinking>...</thinking> and those
	// blocks must not leak into summaries or saved entries.
	transcript = strings.TrimSpace(StripReasoning(transcript))
	if transcript == "" || summarize == nil || a.Project == nil {
		return true
	}
	prompt := "Summarize this coding session in 2-4 short lines: WHAT was done, " +
		"WHY, and which files were touched. Plain text, no markdown, no preamble. " +
		"If nothing meaningful happened, reply exactly: NOTHING.\n" +
		"Additionally, if the conversation reveals DURABLE facts or preferences " +
		"about the user (their name, preferred language, communication style, " +
		"standing preferences), append one line per fact, each starting with " +
		"exactly \"USER: \" (example: USER: The user's name is Anna.). " +
		"Only durable facts about the user — no session details.\n\n" + transcript
	summary, err := summarize(ctx, prompt)
	if err != nil {
		return false
	}
	summary = strings.TrimSpace(StripReasoning(summary))
	if summary == "" || len(summary) > 8000 {
		return true
	}
	summary, userFacts := splitUserFacts(summary)
	a.saveUserFacts(userFacts)
	if summary == "" || strings.EqualFold(summary, "NOTHING") || len(summary) > 4000 {
		return true
	}
	now := time.Now()
	_ = a.Project.Put(Entry{
		ID:      fmt.Sprintf("log-%x", now.UnixNano()),
		Scope:   ScopeTaskLog,
		Content: summary,
		Source:  SourceAgent,
	})
	// Use the first line of the summary as the card description.
	first := summary
	if i := strings.IndexByte(first, '\n'); i > 0 {
		first = first[:i]
	}
	RefreshCard(a.Global, a.ProjectPath, first, "active")
	return true
}

// splitUserFacts separates trailing "USER: ..." lines from the
// task-log summary. Returns the cleaned summary and the facts.
func splitUserFacts(summary string) (string, []string) {
	var rest, facts []string
	for _, line := range strings.Split(summary, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "USER:") {
			if f := strings.TrimSpace(strings.TrimPrefix(t, "USER:")); f != "" {
				facts = append(facts, f)
			}
			continue
		}
		rest = append(rest, line)
	}
	return strings.TrimSpace(strings.Join(rest, "\n")), facts
}

// saveUserFacts writes durable user facts to the GLOBAL store as
// preference entries (the briefing always shows global
// preferences), skipping facts that already have an identical or
// near-identical entry.
func (a *AutoSaver) saveUserFacts(facts []string) {
	if a == nil || a.Global == nil || len(facts) == 0 {
		return
	}
	existing := map[string]struct{}{}
	for _, scope := range []string{ScopePreference, ScopeFact} {
		if entries, err := a.Global.Recent(scope, 100); err == nil {
			for _, e := range entries {
				existing[normalizeFact(e.Content)] = struct{}{}
			}
		}
	}
	for _, f := range facts {
		f = strings.TrimSpace(f)
		if f == "" || len(f) > 500 {
			continue
		}
		norm := normalizeFact(f)
		if _, dup := existing[norm]; dup {
			continue
		}
		if containsSimilar(existing, norm) {
			continue
		}
		_ = a.Global.Put(Entry{
			ID:      fmt.Sprintf("pref-%x", time.Now().UnixNano()),
			Scope:   ScopePreference,
			Content: f,
			Source:  SourceAgent,
		})
		existing[norm] = struct{}{}
	}
}

// normalizeFact lower-cases and strips punctuation/whitespace so
// "The user's name is Anna." == "the users name is anna".
func normalizeFact(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r > 127:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// containsSimilar reports whether an existing normalized entry
// contains (or is contained by) the candidate — a cheap "very
// similar" check that needs no embeddings.
func containsSimilar(existing map[string]struct{}, norm string) bool {
	if len(norm) < 8 {
		return false
	}
	for e := range existing {
		if len(e) < 8 {
			continue
		}
		if strings.Contains(e, norm) || strings.Contains(norm, e) {
			return true
		}
	}
	return false
}
