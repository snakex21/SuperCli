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
	transcript = strings.TrimSpace(transcript)
	if transcript == "" || summarize == nil || a.Project == nil {
		return
	}
	prompt := "Summarize this coding session in 2-4 short lines: WHAT was done, " +
		"WHY, and which files were touched. Plain text, no markdown, no preamble. " +
		"If nothing meaningful happened, reply exactly: NOTHING.\n\n" + transcript
	summary, err := summarize(ctx, prompt)
	if err != nil {
		return
	}
	summary = strings.TrimSpace(summary)
	if summary == "" || strings.EqualFold(summary, "NOTHING") || len(summary) > 4000 {
		return
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
}
