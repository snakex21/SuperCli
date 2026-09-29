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
	"supercli/internal/system/uilang"
)

// listResumableSessions renders the /resume picker text. By default it
// shows sessions from the current project (cwd); when all is true it
// shows every project's sessions. An empty cwd falls back to showing all
// (nothing to filter on).
func listResumableSessions(ctx context.Context, store *session.Store, currentSessionID, cwd string, all bool, languages ...string) (string, error) {
	language := optionalCommandLanguage(languages)
	var recent []session.RecentSession
	var err error
	scope := uilang.Text(language, "app.resume.all_projects")
	if all || cwd == "" {
		recent, err = store.ListRecent(ctx, 10)
	} else {
		recent, err = store.ListRecentByCwd(ctx, cwd, 10)
		scope = uilang.Text(language, "app.resume.this_project")
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
			snippet = uilang.Text(language, "app.resume.empty_user")
		}
		fmt.Fprintf(&b, uilang.Text(language, "app.resume.row"),
			r.ID, r.StartedAt.Format("2006-01-02 15:04"), r.MessageCount, snippet)
		n++
	}
	if n == 0 {
		if !all && cwd != "" {
			return uilang.Text(language, "app.resume.none_project"), nil
		}
		return uilang.Text(language, "app.resume.none"), nil
	}
	return fmt.Sprintf(uilang.Text(language, "app.resume.list"),
		n, scope, b.String()), nil
}

// resumeSession loads the saved model projection into the loop. Context
// preparation is shared with normal turns and runs only before inference.
// Returns a human-readable result line.
func resumeSession(ctx context.Context, loop *agent.Loop, store *session.Store, windowFor func(string) int, id string, languages ...string) (string, error) {
	language := optionalCommandLanguage(languages)
	id = strings.TrimSpace(id)
	loaded, err := store.ReadModelContext(ctx, id)
	if err != nil {
		return "", fmt.Errorf(uilang.Text(language, "app.resume.read_error"), id, err)
	}
	if len(loaded) == 0 {
		return "", fmt.Errorf(uilang.Text(language, "app.resume.not_found"), id)
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
		return "", fmt.Errorf(uilang.Text(language, "app.resume.no_messages"), id)
	}

	// The shared loop prepares long context at the next Run, after the new
	// user instruction is present. Loading a session itself makes no model
	// call and never drops history after a failed summary.

	loop.LoadConversation(msgs)
	if discovered, err := store.ReadDiscoveredTools(ctx, id); err == nil {
		loop.RestoreDiscoveredTools(discovered)
	}

	var b strings.Builder
	fmt.Fprintf(&b, uilang.Text(language, "app.resume.loaded"), id, len(msgs))
	// Show the tail so the user sees where the conversation
	// left off.
	tail := msgs
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	b.WriteString(uilang.Text(language, "app.resume.tail"))
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

// Legacy callers retain English; command handlers pass the persisted UI choice.
func optionalCommandLanguage(languages []string) string {
	if len(languages) > 0 {
		if language := uilang.Normalize(languages[0]); language != "" {
			return language
		}
	}
	return uilang.English
}
