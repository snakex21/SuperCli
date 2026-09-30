package agent

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/agent/ultrawork"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestUltraworkInstructionsAreRunScoped(t *testing.T) {
	p := scriptedProvider("echo", [][]llm.Delta{{{Content: "done"}, {FinishReason: "stop"}}})
	writer := &flakyWriter{}
	loop, err := NewLoop(LoopConfig{
		Provider: p, Registry: tools.NewRegistry(), Writer: writer,
		Ultrawork: &ultrawork.Wiring{Goal: &stubUltraworkGoal{id: "g1", perCall: []int{1, 0}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"ultrawork ship", "explain the result", "ultrawork verify"} {
		ch, err := loop.Run(context.Background(), text)
		if err != nil {
			t.Fatal(err)
		}
		for event := range ch {
			if failure, ok := event.(ErrorEvent); ok {
				t.Fatal(failure.Err)
			}
		}
	}
	if len(p.reqs) != 4 {
		t.Fatalf("requests = %d, want 4", len(p.reqs))
	}
	t.Logf("plain-turn estimated request tokens: %d", estimateRequestTokens(p.reqs[2], nil))
	for i, want := range []bool{true, true, false, true} {
		if got := containsSystemText(p.reqs[i], "ULTRAWORK MODE ACTIVE"); got != want {
			t.Errorf("request %d mode instructions = %v, want %v", i, got, want)
		}
	}
	if !containsSystemText(p.reqs[1], "Sisyphus @1/3") {
		t.Error("continuation lost its reminder")
	}
	for _, i := range []int{2, 3} {
		if containsSystemText(p.reqs[i], "Sisyphus @") {
			t.Errorf("request %d inherited stale continuation", i)
		}
	}
	for _, history := range [][]llm.Message{loop.Messages, writer.messages} {
		for _, msg := range history {
			if msg.Role == llm.RoleSystem && (strings.Contains(msg.Content, "ULTRAWORK MODE ACTIVE") || strings.Contains(msg.Content, "Sisyphus @")) {
				t.Error("run-only instructions entered canonical or persisted history")
			}
		}
	}
}

func TestUltraworkLegacyHistoryDoesNotReactivateMode(t *testing.T) {
	legacy := []llm.Message{
		{Role: llm.RoleSystem, Content: ultrawork.SystemPromptSection()},
		{Role: llm.RoleSystem, Content: "[Sisyphus @1/3] 1 todo(s) still open on the active /goal. Continue with the next one. Do NOT declare done until every task is `done` or explicitly `skipped` via the goal tool."},
		{Role: llm.RoleUser, Content: "What does ULTRAWORK MODE ACTIVE mean?"},
		{Role: llm.RoleTool, Content: "[Sisyphus @1/3] user document"},
	}
	loop, err := NewLoop(LoopConfig{Provider: scriptedProvider("echo", nil), Registry: tools.NewRegistry(), InitialMessages: legacy})
	if err != nil {
		t.Fatal(err)
	}
	if len(loop.Messages) != 2 {
		t.Fatalf("constructor retained %d messages, want only user/tool evidence", len(loop.Messages))
	}
	loop.LoadConversation(legacy)
	if containsSystemText(loop.Messages, "ULTRAWORK MODE ACTIVE") || containsSystemText(loop.Messages, "Sisyphus @") {
		t.Error("legacy mode instructions survived resume")
	}
	if len(loop.Messages) != 2 {
		t.Fatalf("retained %d messages, want user and tool content only", len(loop.Messages))
	}
	if len(legacy) != 4 || legacy[0].Content != ultrawork.SystemPromptSection() {
		t.Error("source archive was mutated")
	}
}

func TestUltraworkTailIsCountedAndSurvivesCompaction(t *testing.T) {
	for _, route := range []RouteMode{RouteCoordinator, RouteAdvisor, RouteChatOnly} {
		t.Run(string(route), func(t *testing.T) {
			loop, err := NewLoop(LoopConfig{Provider: scriptedProvider("echo", nil), Registry: tools.NewRegistry()})
			if err != nil {
				t.Fatal(err)
			}
			loop.route = route
			loop.Messages = []llm.Message{{Role: llm.RoleUser, Content: "ship the change"}, {Role: llm.RoleAssistant, Content: "checked the source"}}
			check := func() {
				t.Helper()
				wire := loop.providerMessages()
				if got, want := loop.estimateNextRequestTokensRaw(), estimateRequestTokens(wire, loop.buildToolDefs()); got != want {
					t.Errorf("request estimate = %d, wire = %d", got, want)
				}
			}
			check()
			loop.ultraworkMode = true
			check()
			loop.ultraworkReminder = "[Sisyphus @1/3] 1 todo(s) still open on the active /goal. Continue with the next one. Do NOT declare done until every task is `done` or explicitly `skipped` via the goal tool."
			check()
			loop.CompactWithSummary("Source checked; change still pending.")
			check()
			if !containsSystemText(loop.providerMessages(), "ULTRAWORK MODE ACTIVE") || !containsSystemText(loop.providerMessages(), "Sisyphus @1/3") {
				t.Error("compaction dropped active run instructions")
			}
			if containsSystemText(loop.Messages, "ULTRAWORK MODE ACTIVE") || containsSystemText(loop.Messages, "Sisyphus @") {
				t.Error("compaction persisted run-only instructions")
			}
		})
	}
}
