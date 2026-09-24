package agent

import (
	"context"
	"strings"

	"supercli/internal/llm"
)

// prepareRunRoute decides the turn's route and attaches one-shot addons that
// only apply on the coordinator path (repo preflight, verification hint).
// Called once at the start of run(), after the user message is already on
// Messages. Routing uses the raw prompt; the session store keeps that raw
// prompt — addons are not persisted.
func (l *Loop) prepareRunRoute(ctx context.Context, prompt string) {
	// A1: the navigator is a full LLM call. It runs here, inside the
	// background goroutine, so Run() returns immediately and the TUI
	// never blocks on Enter waiting for the route decision.
	switch {
	case !l.navigate:
		// Navigator off: everything is coordinator (safe default for
		// scripted/worker use, which must keep the full tool context).
		l.route = RouteCoordinator
	case l.navAuto:
		// Auto: take the cheap keyword decision on obvious turns and
		// only pay for the navigator model on ambiguous ones. Saves a
		// full model round-trip per confident turn.
		if mode, confident := l.routeMap.ClassifyConfident(prompt); confident {
			l.route = mode
		} else if l.navKeywordsOnly {
			l.route = l.routeMap.Classify(prompt)
		} else {
			l.route = l.navigateRoute(ctx, prompt)
		}
	default:
		l.route = l.navigateRoute(ctx, prompt)
	}
	// Repository context is useful only on the full coordinator route. Keep it
	// queued across chat/advisor turns and attach it to the newest user message
	// immediately before the first coordinator provider call. Routing above saw
	// only the user's raw prompt, and the session store keeps that raw prompt.
	if l.route == RouteCoordinator && l.nextCoordinatorAddon != "" {
		for i := len(l.Messages) - 1; i >= 0; i-- {
			if l.Messages[i].Role == llm.RoleUser {
				l.Messages[i].Content += "\n\n" + l.nextCoordinatorAddon
				l.invalidateVisibleEstimate()
				break
			}
		}
		l.nextCoordinatorAddon = ""
	}
	if l.route == RouteCoordinator && l.wordDocumentContext(prompt) {
		// These are registered but normally invisible. Promote them before the
		// first provider call so a Word request never spends a round trip on
		// tool_search and never sees scripting as the only usable route.
		l.registry.Activate("read_docx", "edit_docx")
	}
	// Verification is a variable, one-shot user-message hint, never part of the
	// cacheable system prefix. It is injected only for explicit mutation work;
	// project questions and ordinary chat pay zero tokens for it.
	if l.route == RouteCoordinator {
		if hint := implementationVerificationHint(prompt); hint != "" {
			for i := len(l.Messages) - 1; i >= 0; i-- {
				if l.Messages[i].Role == llm.RoleUser {
					l.Messages[i].Content += "\n\n" + hint
					l.invalidateVisibleEstimate()
					break
				}
			}
		}
	}
}

func (l *Loop) wordDocumentContext(prompt string) bool {
	if containsWordDocumentReference(prompt) {
		return true
	}
	// Web sessions construct a fresh Loop for each request, so carry the signal
	// from recent user text or prior native tool calls such as "popraw jeszcze".
	start := len(l.Messages) - 12
	if start < 0 {
		start = 0
	}
	for _, message := range l.Messages[start:] {
		if message.Role == llm.RoleUser && containsWordDocumentReference(message.Content) {
			return true
		}
		if message.Role == llm.RoleAssistant {
			for _, call := range message.ToolCalls {
				if call.Name == "read_docx" || call.Name == "edit_docx" {
					return true
				}
			}
		}
	}
	return false
}

func containsWordDocumentReference(s string) bool {
	s = " " + strings.ToLower(strings.TrimSpace(s)) + " "
	for _, marker := range []string{".docx", " word ", " worda", " wordzie", " dokumencie word", " dokument word"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// continueWithDiscoveredTools restores project instructions and normal tools
// after a lightweight turn discovers a capability. This uses the already
// required tool-result continuation; no router call or repeated user prompt.
func (l *Loop) continueWithDiscoveredTools(calls []llm.ToolCall, outcomes []callOutcome) {
	if l.route == RouteCoordinator || len(l.registry.DiscoveredNames()) == 0 {
		return
	}
	for i, call := range calls {
		if call.Name != "tool_search" || i >= len(outcomes) || outcomes[i].failed {
			continue
		}
		l.route = RouteCoordinator
		if l.nextCoordinatorAddon != "" {
			l.Messages = append(l.Messages, llm.Message{Role: llm.RoleSystem, Content: l.nextCoordinatorAddon})
			l.nextCoordinatorAddon = ""
			l.invalidateVisibleEstimate()
		}
		return
	}
}
