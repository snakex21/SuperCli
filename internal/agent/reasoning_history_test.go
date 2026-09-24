package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestReasoningHistoryDefaultDoesNotReplayAcrossRuns(t *testing.T) {
	t.Setenv("SUPERCLI_KEEP_THINKING", "")
	p := &stubProvider{name: "reasoning-history", scripts: [][]llm.Delta{
		{{Reasoning: "reasoning-only-marker"}, {Content: "visible answer", FinishReason: "stop"}},
		{{Content: "second answer", FinishReason: "stop"}},
	}}
	w := &recordingWriter{}
	l, err := NewLoop(LoopConfig{Provider: p, Registry: emptyRegistry(), Writer: w})
	if err != nil {
		t.Fatal(err)
	}
	var displayed strings.Builder
	for _, prompt := range []string{"first task", "second task"} {
		events, err := l.Run(context.Background(), prompt)
		if err != nil {
			t.Fatal(err)
		}
		for ev := range events {
			if e, ok := ev.(ErrorEvent); ok {
				t.Fatal(e.Err)
			}
			if e, ok := ev.(ReasoningEvent); ok {
				displayed.WriteString(e.Text)
			}
		}
	}
	if len(p.reqs) != 2 {
		t.Fatalf("calls=%d, want 2", len(p.reqs))
	}
	if strings.Contains(reqText(p.reqs[1]), "reasoning-only-marker") {
		t.Error("reasoning replayed in the next request")
	}
	if !strings.Contains(reqText(p.reqs[1]), "visible answer") {
		t.Error("visible answer lost")
	}
	if displayed.String() != "reasoning-only-marker" || !strings.Contains(reqText(w.messages), "reasoning-only-marker") {
		t.Error("reasoning lost from live UI or persisted transcript")
	}
}

func TestReasoningHistoryInitialAndResumePreserveArchiveAndTools(t *testing.T) {
	t.Setenv("SUPERCLI_KEEP_THINKING", "")
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "Explain literal <think>user example</think>"},
		{Role: llm.RoleAssistant, Content: "<think>old reasoning marker</think>visible answer"},
		{Role: llm.RoleAssistant, Parts: []llm.ContentPart{
			{Type: llm.PartTypeText, Text: "<thinking>tool reasoning marker</thinking>"},
			{Type: llm.PartTypeImage, Image: &llm.ImageRef{Data: "AAA", MediaType: "image/png"}},
		}, ToolCalls: []llm.ToolCall{{ID: "read-1", Name: "read_lines", Arguments: "{}"}}},
		{Role: llm.RoleTool, ToolCallID: "read-1", Content: "<think>literal tool output</think>"},
	}
	before := reqText(history)
	for _, resume := range []bool{false, true} {
		cfg := LoopConfig{Provider: makeScriptedProvider("done"), Registry: emptyRegistry()}
		if !resume {
			cfg.InitialMessages = history
		}
		l, err := NewLoop(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if resume {
			l.LoadConversation(history)
		}
		text := reqText(l.providerMessages())
		for _, marker := range []string{"old reasoning marker", "tool reasoning marker"} {
			if strings.Contains(text, marker) {
				t.Errorf("resume=%v: replayed %s", resume, marker)
			}
		}
		for _, want := range []string{"visible answer", "<think>user example</think>", "<think>literal tool output</think>"} {
			if !strings.Contains(text, want) {
				t.Errorf("resume=%v: lost %s", resume, want)
			}
		}
		if !reflect.DeepEqual(l.Messages[2].ToolCalls, history[2].ToolCalls) || l.Messages[3].ToolCallID != "read-1" {
			t.Error("tool call/result pairing changed")
		}
		if len(l.Messages[2].Parts) != 1 || !reflect.DeepEqual(l.Messages[2].Parts[0], history[2].Parts[1]) {
			t.Error("image lost or empty text part retained")
		}
		if reqText(history) != before {
			t.Error("archive mutated while preparing model history")
		}
	}
}

func TestUndecidedChatRouting(t *testing.T) {
	routes := DefaultRouteMap()
	for _, prompt := range []string{
		"narazie nie wiem właśnie zastanawiam się", "Na razie nie wiem, właśnie zastanawiam się.",
		"jeszcze nie wiem", "Zastanawiam się…", "wlasnie sie zastanawiam", "nie wiem jeszcze",
	} {
		mode, confident := routes.ClassifyConfident(prompt)
		if mode != RouteChatOnly || !confident {
			t.Errorf("%q: %s confident=%v", prompt, mode, confident)
		}
	}
	for _, prompt := range []string{
		"nie wiem, sprawdź", "na razie nie wiem, napraw testy", "zastanawiam się nad kodem", "nie wiem co jest w repo",
		"nie wiem, kontynuuj", "narazie nie wiem właśnie zastanawiam się, zapisz to", "jeszcze", "dalej", "tak",
	} {
		if mode, _ := routes.ClassifyConfident(prompt); mode != RouteCoordinator {
			t.Errorf("task or ambiguous %q routed to %s", prompt, mode)
		}
	}
}

func TestNativeReasoningSurvivesLiveAndResumedTurnsWithoutPromptCopy(t *testing.T) {
	t.Setenv("SUPERCLI_KEEP_THINKING", "1") // legacy opt-in must not duplicate native state
	block := &llm.ReasoningBlock{Format: llm.ReasoningChat, Model: "native-fixture", Scope: "fixture-scope", Data: []byte("{\"reasoning_content\":\"native-only-marker\"}")}
	p := &stubProvider{name: "native-fixture", scripts: [][]llm.Delta{
		{{Reasoning: "native-only-marker"}, {NativeReasoning: block}, {Content: "visible answer", FinishReason: "stop", Usage: &llm.Usage{Reasoning: 12}}},
		{{Content: "continued", FinishReason: "stop"}},
	}}
	w := &recordingWriter{}
	l, err := NewLoop(LoopConfig{Provider: p, Registry: emptyRegistry(), Writer: w})
	if err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{"first task", "continue"} {
		ch, err := l.Run(context.Background(), prompt)
		if err != nil {
			t.Fatal(err)
		}
		for ev := range ch {
			if e, ok := ev.(ErrorEvent); ok {
				t.Fatal(e.Err)
			}
		}
	}
	if len(p.reqs) != 2 {
		t.Fatalf("requests=%d", len(p.reqs))
	}
	check := func(msgs []llm.Message) {
		t.Helper()
		count := 0
		for _, m := range msgs {
			for _, part := range m.Parts {
				if part.Type == llm.PartTypeReasoning {
					count++
					if string(part.Reasoning.Data) != string(block.Data) || part.Reasoning.Tokens != 12 {
						t.Fatal("native payload or accounting lost")
					}
				}
			}
		}
		if count != 1 {
			t.Fatalf("native blocks=%d, want one", count)
		}
		if strings.Contains(reqText(msgs), "native-only-marker") || strings.Contains(reqText(msgs), "Retained reasoning") {
			t.Fatal("native reasoning copied into text prompt")
		}
	}
	check(p.reqs[1])
	resumed, err := NewLoop(LoopConfig{Provider: p, Registry: emptyRegistry(), InitialMessages: w.messages})
	if err != nil {
		t.Fatal(err)
	}
	check(resumed.providerMessages())
	resumed.LoadConversation(w.messages)
	check(resumed.providerMessages())
	if !strings.Contains(reqText(w.messages), "<thinking>native-only-marker</thinking>") {
		t.Fatal("UI transcript lost")
	}
}

func TestNativeReasoningIsNotAVisibleReplyOrDisposableEnvelope(t *testing.T) {
	b := &llm.ReasoningBlock{Format: llm.ReasoningResponses, Model: "fixture", Scope: "fixture",
		Data: json.RawMessage("{\"type\":\"reasoning\",\"encrypted_content\":\"opaque\"}")}
	hidden := llm.Message{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeReasoning, Reasoning: b}}}
	if messageHasVisibleReply(hidden) {
		t.Fatal("opaque reasoning counted as a user answer")
	}
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "work"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "read", Name: "read_lines", Arguments: "{}"}}},
		{Role: llm.RoleTool, ToolCallID: "read", Content: "evidence"},
		hidden,
	}
	if !messagesHaveRecentToolResult(messages) {
		t.Fatal("reasoning hid an unresolved result")
	}
	messages = append(messages, llm.Message{Role: llm.RoleAssistant, Content: "Completed."})
	if got := omitResolvedToolHistory(messages); len(got) != len(messages) {
		t.Fatal("native continuation lost its tool evidence")
	}
}

func TestModelSwitchDoesNotBudgetIncompatibleReasoning(t *testing.T) {
	p, err := llm.NewOpenAI(llm.OpenAIConfig{BaseURL: "http://127.0.0.1:9/v1", Model: "new-model"})
	if err != nil {
		t.Fatal(err)
	}
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "previous task"},
		{Role: llm.RoleAssistant, Parts: []llm.ContentPart{
			{Type: llm.PartTypeReasoning, Reasoning: &llm.ReasoningBlock{Format: llm.ReasoningChat, Model: "old-model", Scope: "old-endpoint", Tokens: 50000, Data: json.RawMessage("{\"reasoning_content\":\"old reasoning\"}")}},
			{Type: llm.PartTypeText, Text: "Previous result."},
		}},
	}
	l, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), InitialMessages: history})
	if err != nil {
		t.Fatal(err)
	}
	if estimate := l.EstimateNextRequestTokens(); estimate >= 50000 {
		t.Fatalf("filtered reasoning bloated the handoff estimate: %d", estimate)
	}
	if !l.Messages[1].HasNativeReasoning() {
		t.Fatal("request projection modified history")
	}
}
