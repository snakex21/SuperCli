// /resume — list recent sessions and load one back into the
// live agent loop. Huge sessions are summarized via the same
// machinery as /compact: old messages collapse into a summary,
// recent ones are kept verbatim.
package app

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

// listResumableSessions renders the /resume picker text. By default it
// shows sessions from the current project (cwd); when all is true it
// shows every project's sessions. An empty cwd falls back to showing all
// (nothing to filter on).
func listResumableSessions(ctx context.Context, store *session.Store, currentSessionID, cwd string, all bool) (string, error) {
	var recent []session.RecentSession
	var err error
	scope := "all projects"
	if all || cwd == "" {
		recent, err = store.ListRecent(ctx, 10)
	} else {
		recent, err = store.ListRecentByCwd(ctx, cwd, 10)
		scope = "this project"
	}
	if err != nil {
		return "", err
	}
	var b strings.Builder
	n := 0
	for _, r := range recent {
		if r.ID == currentSessionID {
			continue
		}
		snippet := strings.Join(strings.Fields(r.FirstUserMsg), " ")
		if len(snippet) > 60 {
			snippet = snippet[:59] + "…"
		}
		if snippet == "" {
			snippet = "(no user message)"
		}
		fmt.Fprintf(&b, "  %s  %s  %3d msg  %s\n",
			r.ID, r.StartedAt.Format("2006-01-02 15:04"), r.MessageCount, snippet)
		n++
	}
	if n == 0 {
		if !all && cwd != "" {
			return "resume: no previous sessions in this project (try /resume all)", nil
		}
		return "resume: no previous sessions found", nil
	}
	return fmt.Sprintf("%d recent session(s) — %s:\n%susage: /resume <session-id>  ·  /resume all to list every project",
		n, scope, b.String()), nil
}

// resumeSession loads the saved model projection into the loop. Context
// preparation is shared with normal turns and runs only before inference.
// Returns a human-readable result line.
func resumeSession(ctx context.Context, loop *agent.Loop, store *session.Store, windowFor func(string) int, id string) (string, error) {
	id = strings.TrimSpace(id)
	loaded, err := store.ReadModelContext(ctx, id)
	if err != nil {
		return "", fmt.Errorf("resume: read %s: %w", id, err)
	}
	if len(loaded) == 0 {
		return "", fmt.Errorf("resume: session %q not found or empty", id)
	}
	// Decode, dropping the leading system run (the old base
	// prompt / pattern injection — the live loop has its own).
	// Later system messages (compaction summaries, reflections)
	// are kept: they carry conversation state.
	var msgs []llm.Message
	leading := true
	for _, m := range loaded {
		if leading && m.Role == llm.RoleSystem {
			continue
		}
		leading = false
		msgs = append(msgs, m)
	}
	if len(msgs) == 0 {
		return "", fmt.Errorf("resume: session %q has no loadable messages", id)
	}

	// The shared loop prepares long context at the next Run, after the new
	// user instruction is present. Loading a session itself makes no model
	// call and never drops history after a failed summary.

	loop.LoadConversation(msgs)
	if discovered, err := store.ReadDiscoveredTools(ctx, id); err == nil {
		loop.RestoreDiscoveredTools(discovered)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "resumed session %s: %d message(s) loaded", id, len(msgs))
	// Show the tail so the user sees where the conversation
	// left off.
	tail := msgs
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	b.WriteString("\n--- last messages ---")
	for _, m := range tail {
		text := strings.TrimSpace(m.Content)
		if text == "" {
			for _, p := range m.Parts {
				if p.Type == llm.PartTypeText {
					text += p.Text
				}
			}
			text = strings.TrimSpace(text)
		}
		if text == "" {
			continue
		}
		text = strings.Join(strings.Fields(text), " ")
		if len(text) > 200 {
			text = text[:199] + "…"
		}
		fmt.Fprintf(&b, "\n[%s] %s", m.Role, text)
	}
	return b.String(), nil
}
