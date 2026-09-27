package agent

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/llm"
)

func (l *Loop) navigateRoute(ctx context.Context, prompt string) RouteMode {
	fallback := l.routeMap.Classify(prompt)
	msgs := l.navigatorMessages(prompt)
	// Classification runs on the small side provider when one is wired
	// (task_model worker / draft provider) so the navigator's prompt —
	// a different prefix — never evicts the main conversation from a
	// single-slot llama.cpp KV cache. Errors (including a dead side
	// host) degrade to the keyword fallback, exactly like main-provider
	// errors always have — a broken navigator never breaks the turn.
	prov := l.provider
	if l.navProvider != nil {
		prov = l.navProvider
	}
	// The route classification is a full helper inference that delays
	// the user's first token. Book its wall time like every other aux
	// call (recordAuxWall) — it runs before step 1 opens, so it lands
	// on that turn's aux counter instead of vanishing.
	navStart := time.Now()
	stream, err := prov.Complete(llm.WithPurpose(ctx, llm.PurposeNavigator), msgs, nil)
	if err != nil {
		l.recordAuxWall(llm.PurposeNavigator, time.Since(navStart))
		return fallback
	}
	var text strings.Builder
	for d := range stream {
		if d.Err != nil {
			l.recordAuxWall(llm.PurposeNavigator, time.Since(navStart))
			return fallback
		}
		text.WriteString(d.Content)
	}
	l.recordAuxWall(llm.PurposeNavigator, time.Since(navStart))
	mode, ok := parseNavigatorMode(text.String())
	if !ok {
		return fallback
	}
	return mode
}

func (l *Loop) navigatorMessages(prompt string) []llm.Message {
	visible := l.VisibleMessages()
	out := []llm.Message{{Role: llm.RoleSystem, Content: navigatorSystemPrompt}}
	end, limit := len(visible), 4
	// Run has already appended the current user message. Send its raw prompt
	// once below, without replaying its truncated copy or one-shot addons.
	if end > 0 && isConversationUserTurn(visible[end-1]) {
		end--
		limit--
	}
	tail := make([]llm.Message, 0, limit)
	for i := end - 1; i >= 0 && len(tail) < limit; i-- {
		m := visible[i]
		if m.Role != llm.RoleAssistant && !isConversationUserTurn(m) {
			continue
		}
		if strings.Contains(m.Content, "<task-notification>") || len(m.ToolCalls) > 0 {
			continue
		}
		text := navigatorHistoryText(m)
		if text != "" {
			tail = append(tail, llm.Message{Role: m.Role, Content: text})
		}
	}
	for i := len(tail) - 1; i >= 0; i-- {
		out = append(out, tail[i])
	}
	out = append(out, llm.Message{Role: llm.RoleUser, Content: prompt})
	return out
}

// Classification needs dialogue and attachment labels, not pixels or another
// model's continuation state. Bound the whole message, not each part separately;
// keep the canonical transcript and image activation flags untouched.
func navigatorHistoryText(m llm.Message) string {
	text := m.Content
	if len(m.Parts) > 0 {
		var b strings.Builder
		for _, part := range m.Parts {
			var s string
			switch part.Type {
			case llm.PartTypeText:
				s = part.Text
			case llm.PartTypeImage:
				s = "[image]"
				if part.Image != nil {
					name := part.Image.Name
					if name == "" && part.Image.Path != "" {
						name = filepath.Base(part.Image.Path)
					}
					if name != "" {
						s = "[image: " + name + "]"
					}
				}
			}
			if s != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(s)
			}
		}
		text = b.String()
	}
	if m.Role == llm.RoleAssistant {
		text = stripThinking(text)
	}
	return truncateForNavigator(strings.TrimSpace(text))
}

func truncateForNavigator(s string) string {
	const max = 500
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func parseNavigatorMode(s string) (RouteMode, bool) {
	s = strings.ToLower(s)
	// Strip common thinking wrappers if the model exposes reasoning text.
	if idx := strings.LastIndex(s, "{"); idx >= 0 {
		s = s[idx:]
	}
	switch {
	case strings.Contains(s, `"mode":"chat"`) || strings.Contains(s, `"mode": "chat"`):
		return RouteChatOnly, true
	case strings.Contains(s, `"mode":"advisor"`) || strings.Contains(s, `"mode": "advisor"`):
		return RouteAdvisor, true
	case strings.Contains(s, `"mode":"coordinator"`) || strings.Contains(s, `"mode": "coordinator"`):
		return RouteCoordinator, true
	case strings.Contains(s, `"mode":"clarify"`) || strings.Contains(s, `"mode": "clarify"`):
		return RouteClarify, true
	default:
		return "", false
	}
}
