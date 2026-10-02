package webgui

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

// shouldQueuePreflight installs a lazy source until this session has had a
// project turn. Actual collection is decided by Loop's route, not a second
// frontend classifier. The marker also covers a light turn promoted by tools
// or an attachment; older transcripts retain the conservative keyword fallback.
func shouldQueuePreflight(initial []llm.Message) bool {
	routes := agent.DefaultRouteMap()
	for _, msg := range initial {
		if msg.Role != llm.RoleUser || strings.Contains(msg.Content, "<task-notification>") {
			continue
		}
		if strings.Contains(msg.TextOnly().Content, "Repo state (auto-collected):") {
			return false
		}
		mode, confident := routes.ClassifyConfident(msg.Content)
		if !confident || mode == agent.RouteCoordinator {
			return false
		}
	}
	return true
}

// sessionState opens the persistent session store, creates a new session when
// no id was supplied, or loads existing messages when the browser continues a
// chat. The returned close function must be called after the run completes.
func (e *Engine) sessionState(ctx context.Context, prompt, requestedID, home string) ([]llm.Message, agent.SessionWriter, string, error) {
	store, err := e.sessionStore()
	if err != nil {
		return nil, nil, "", fmt.Errorf("open session store: %w", err)
	}

	requestedID = strings.TrimSpace(requestedID)
	provider, model, reasoning := e.RuntimeSelection()
	if requestedID == "" {
		// The first title is deterministic and local — set right here,
		// zero inference, so the session is named instantly and the
		// model slot stays free for the actual answer. The nicer LLM
		// title runs later, after the stream + idle (see title.go).
		title := summarizeHistoryMessage(prompt, sessionTitleMaxRunes)
		sess, err := store.Create(home, model, title)
		if err != nil {
			return nil, nil, "", fmt.Errorf("create session: %w", err)
		}
		if err := store.SetRuntime(sess.ID, provider, model, reasoning); err != nil {
			return nil, nil, "", fmt.Errorf("save session runtime: %w", err)
		}
		return nil, session.NewWriter(store, sess.ID), sess.ID, nil
	}

	meta, err := store.Get(requestedID)
	if err != nil {
		return nil, nil, "", fmt.Errorf("resume session: %w", err)
	}
	if !sameSessionWorkspace(meta.Cwd, home) {
		return nil, nil, "", fmt.Errorf("resume session: %w", errSessionOutsideWorkspace)
	}
	if err := store.SetRuntime(requestedID, provider, model, reasoning); err != nil {
		return nil, nil, "", fmt.Errorf("save session runtime: %w", err)
	}
	initial, err := store.ReadModelContext(ctx, requestedID)
	if err != nil {
		return nil, nil, "", fmt.Errorf("read session messages: %w", err)
	}
	return initial, session.NewWriter(store, requestedID), requestedID, nil
}
