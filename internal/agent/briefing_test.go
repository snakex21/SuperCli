package agent

import (
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// TestChatRouteKeepsBriefing is the regression test for the
// "model forgets the user's name in smalltalk" bug: the chat-only
// route replaces the full system prompt, so the memory briefing
// must be re-appended to the minimal chat/advisor prompts.
func TestChatRouteKeepsBriefing(t *testing.T) {
	briefing := "[memory_briefing]\nUser preferences:\n- The user's name is Maksymilian\n[/memory_briefing]"
	l, err := NewLoop(LoopConfig{
		Provider: echoProvider("p"),
		Registry: tools.NewRegistry(),
		System:   "full system prompt\n\n" + briefing,
		Briefing: briefing,
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleUser, Content: "cześć"})

	for _, route := range []RouteMode{RouteChatOnly, RouteAdvisor, RouteClarify, RouteCoordinator} {
		l.route = route
		msgs := l.providerMessages()
		if len(msgs) == 0 || msgs[0].Role != llm.RoleSystem {
			t.Fatalf("route %s: no system message", route)
		}
		if !strings.Contains(msgs[0].Content, "Maksymilian") {
			t.Errorf("route %s: system prompt lost the memory briefing:\n%s", route, msgs[0].Content)
		}
	}
}
