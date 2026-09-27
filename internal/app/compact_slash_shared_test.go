package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/ui/tui"
)

func TestCompactSlashUsesSharedCompaction(t *testing.T) {
	provider, _ := llm.NewEcho("compact-fixture")
	registry := tools.NewRegistry()
	calls := 0
	var summarized []llm.Message
	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider: provider, Registry: registry, WindowFor: func(string) int { return 100_000 },
		InitialMessages: []llm.Message{
			{Role: llm.RoleUser, Content: "HIDDEN-OLD"},
			{Role: llm.RoleAssistant, Content: "HIDDEN-ANSWER"},
			{Role: llm.RoleUser, Content: "Visible old task"},
			{Role: llm.RoleAssistant, Content: strings.Repeat("verified finding ", 2000)},
			{Role: llm.RoleUser, Content: "Keep this correction"},
			{Role: llm.RoleAssistant, Content: "Previous result"},
			{Role: llm.RoleUser, Content: "Current request"},
		},
		Summarizer: func(_ context.Context, _ llm.Provider, msgs []llm.Message) (string, error) {
			calls++
			summarized = append([]llm.Message(nil), msgs...)
			return agent.WrapCompactSummary("Done: visible investigation. Pending: current request."), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := loop.HideRange(0, 2); err != nil {
		t.Fatal(err)
	}
	commands := map[string]tui.SlashHandler{}
	wireSlashEarly(commands, slashWireDeps{loop: loop, registry: registry})
	result, err := commands["compact"](context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("configured shared summarizer called %d times; result=%s", calls, result)
	}
	input := agent.RenderCompactTranscript(summarized)
	if strings.Contains(input, "HIDDEN-") || strings.Contains(input, "Current request") || strings.Contains(input, "Keep this correction") {
		t.Fatalf("unexpected summary input: %.200s", input)
	}
	view := agent.RenderCompactTranscript(loop.VisibleMessages())
	for _, want := range []string{"Done: visible investigation", "Keep this correction", "Previous result", "Current request"} {
		if !strings.Contains(view, want) {
			t.Errorf("lost %q", want)
		}
	}
	if strings.Contains(view, "HIDDEN-") {
		t.Error("hidden context returned")
	}
	if !strings.Contains(result, "replaced") {
		t.Errorf("bad command response: %s", result)
	}
}

func TestCompactSlashPreservesContextOnSharedFailure(t *testing.T) {
	provider, _ := llm.NewEcho("compact-fixture")
	loop, err := agent.NewLoop(agent.LoopConfig{
		Provider: provider, Registry: tools.NewRegistry(), WindowFor: func(string) int { return 100_000 },
		InitialMessages: []llm.Message{
			{Role: llm.RoleUser, Content: "older request"},
			{Role: llm.RoleAssistant, Content: strings.Repeat("earlier evidence ", 1000)},
			{Role: llm.RoleUser, Content: "previous instruction"},
			{Role: llm.RoleAssistant, Content: "previous answer"},
			{Role: llm.RoleUser, Content: "current instruction"},
		},
		Summarizer: func(context.Context, llm.Provider, []llm.Message) (string, error) {
			return "", errors.New("fixture summarizer unavailable")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	before := agent.RenderCompactTranscript(loop.VisibleMessages())
	commands := map[string]tui.SlashHandler{}
	wireSlashEarly(commands, slashWireDeps{loop: loop, registry: tools.NewRegistry()})
	result, err := commands["compact"](context.Background(), "")
	if err != nil || !strings.Contains(result, "fixture summarizer unavailable") {
		t.Fatalf("missing shared failure: %s %v", result, err)
	}
	if got := agent.RenderCompactTranscript(loop.VisibleMessages()); got != before {
		t.Fatal("failed /compact changed history")
	}
}
