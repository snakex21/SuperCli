// /resume — list recent sessions and load one back into the
// live agent loop. Huge sessions are summarized via the same
// machinery as /compact: old messages collapse into a summary,
// recent ones are kept verbatim.
package main

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

// resumeKeepRecent is how many trailing messages are kept
// verbatim when an oversized session is summarized.
const resumeKeepRecent = 20

// listResumableSessions renders the /resume picker text.
func listResumableSessions(ctx context.Context, store *session.Store, currentSessionID string) (string, error) {
	recent, err := store.ListRecent(ctx, 10)
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
		return "resume: no previous sessions found", nil
	}
	return fmt.Sprintf("%d recent session(s):\n%susage: /resume <session-id>", n, b.String()), nil
}

// resumeSession loads session id from store into loop. When the
// decoded conversation is too large for the model's window, the
// older part is summarized (via the loop's active provider) and
// only the last resumeKeepRecent messages are kept verbatim.
// Returns a human-readable result line.
func resumeSession(ctx context.Context, loop *agent.Loop, store *session.Store, windowFor func(string) int, id string) (string, error) {
	id = strings.TrimSpace(id)
	enc, err := store.ReadMessages(ctx, id)
	if err != nil {
		return "", fmt.Errorf("resume: read %s: %w", id, err)
	}
	if len(enc) == 0 {
		return fmt.Sprintf("resume: session %q not found or empty", id), nil
	}
	// Decode, dropping the leading system run (the old base
	// prompt / pattern injection — the live loop has its own).
	// Later system messages (compaction summaries, reflections)
	// are kept: they carry conversation state.
	var msgs []llm.Message
	leading := true
	for _, e := range enc {
		m, err := e.ToMessage()
		if err != nil {
			continue // defensive: skip rows that no longer decode
		}
		if leading && m.Role == llm.RoleSystem {
			continue
		}
		leading = false
		msgs = append(msgs, m)
	}
	if len(msgs) == 0 {
		return fmt.Sprintf("resume: session %q has no loadable messages", id), nil
	}

	// Size gate: if the resumed conversation alone would eat
	// more than 60% of the window, summarize the old part.
	window := 16384
	if windowFor != nil {
		if w := windowFor(loop.CurrentModel()); w > 0 {
			window = w
		}
	}
	summarized := 0
	if estimateMessagesTokens(msgs) > window*6/10 && len(msgs) > resumeKeepRecent {
		cut := len(msgs) - resumeKeepRecent
		// Don't start the verbatim tail on an orphan tool
		// result: advance past consecutive tool messages.
		for cut < len(msgs) && msgs[cut].Role == llm.RoleTool {
			cut++
		}
		older, recent := msgs[:cut], msgs[cut:]
		if summary, err := summarizeForCompaction(ctx, loop.Provider(), older); err == nil {
			msgs = append([]llm.Message{{
				Role:    llm.RoleSystem,
				Content: wrapCompactSummary(summary),
			}}, recent...)
			summarized = len(older)
		} else {
			// Summarization failed; keep only the recent
			// tail rather than overflowing the window.
			msgs = recent
		}
	}

	loop.LoadConversation(msgs)

	var b strings.Builder
	fmt.Fprintf(&b, "resumed session %s: %d message(s) loaded", id, len(msgs))
	if summarized > 0 {
		fmt.Fprintf(&b, " (%d older message(s) summarized)", summarized)
	}
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

// estimateMessagesTokens is the chars/4 heuristic over a
// message slice (same approach as Loop.EstimateVisibleTokens).
func estimateMessagesTokens(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content) / 4
		for _, p := range m.Parts {
			if p.Type == llm.PartTypeText {
				n += len(p.Text) / 4
			}
		}
	}
	return n
}
