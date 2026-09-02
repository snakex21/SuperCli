package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestHardProtocolPostToolEmptyRepliesNeverEndDone(t *testing.T) {
	toolTurn := func(id string) []llm.Delta {
		return []llm.Delta{
			echoCall(id),
			{FinishReason: "tool_calls"},
		}
	}
	emptyTurn := []llm.Delta{{Role: llm.RoleAssistant}, {FinishReason: "stop"}}
	provider := &stubProvider{name: "hard-empty-after-tools", scripts: [][]llm.Delta{
		toolTurn("hard-1"), emptyTurn,
		toolTurn("hard-2"), emptyTurn,
		toolTurn("hard-3"), emptyTurn,
	}}
	loop, err := NewLoop(LoopConfig{
		Provider: provider,
		Registry: echoToolRegistry(t),
		MaxSteps: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := loop.Run(context.Background(), "use tools and then answer me")
	if err != nil {
		t.Fatal(err)
	}
	events := drainEvents(t, stream)
	var doneCount, errorCount, nudgeCount int
	var terminalError string
	for _, event := range events {
		switch value := event.(type) {
		case DoneEvent:
			doneCount++
		case ErrorEvent:
			errorCount++
			if value.Err != nil {
				terminalError = value.Err.Error()
			}
		case NoticeEvent:
			if strings.Contains(value.Text, "forcing a user-facing answer") {
				nudgeCount++
			}
		}
	}
	if doneCount != 0 || errorCount != 1 || nudgeCount != 2 || !strings.Contains(terminalError, "empty user-facing reply") {
		t.Fatalf("done=%d errors=%d nudges=%d terminal=%q events=%+v messages=%+v", doneCount, errorCount, nudgeCount, terminalError, events, loop.Messages)
	}
}

func TestHardProtocolResolvedToolsLeavePromptButStayInTranscript(t *testing.T) {
	toolCall := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
		ID: "mail-1", Name: "thunderbird_mail", Arguments: `{"op":"search","text":"faktura"}`,
	}}}
	toolResult := llm.Message{Role: llm.RoleTool, ToolCallID: "mail-1", Name: "thunderbird_mail", Content: strings.Repeat("large mail result ", 2000)}
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "znajdz fakture"},
		toolCall,
		toolResult,
		{Role: llm.RoleAssistant, Content: "Znalazlem fakture i podsumowalem wynik."},
		{Role: llm.RoleUser, Content: "co dalej?"},
	}

	projected := omitResolvedToolHistory(messages)
	if len(projected) != 3 {
		t.Fatalf("provider projection has %d messages, want user/final/user: %+v", len(projected), projected)
	}
	for _, message := range projected {
		if message.Role == llm.RoleTool || len(message.ToolCalls) > 0 || strings.Contains(message.Content, "large mail result") {
			t.Fatalf("resolved tool protocol leaked into provider projection: %+v", message)
		}
	}
	if len(messages) != 5 || messages[1].ToolCalls[0].ID != "mail-1" || messages[2].Content != toolResult.Content {
		t.Fatal("canonical transcript was mutated while building the provider projection")
	}

	unresolved := omitResolvedToolHistory(messages[:3])
	if len(unresolved) != 3 || unresolved[1].ToolCalls[0].ID != "mail-1" || unresolved[2].Role != llm.RoleTool {
		t.Fatalf("unresolved tool tail was compacted: %+v", unresolved)
	}
}

func TestHardProtocolResolvedToolsAreSentOnceThenRetrievedOnDemand(t *testing.T) {
	provider := &stubProvider{name: "hard-resolved-context", scripts: [][]llm.Delta{
		{echoCall("resolved-1"), {FinishReason: "tool_calls"}},
		{{Content: "Pierwsza praca zakonczona."}, {FinishReason: "stop"}},
		{{Content: "Odpowiadam na kolejne pytanie."}, {FinishReason: "stop"}},
	}}
	registry := echoToolRegistry(t)
	registry.MustRegister(tools.Tool{
		Name: "search_history", Description: "retrieve persisted transcript", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "history"}, nil
		},
	})
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, MaxSteps: 5})
	if err != nil {
		t.Fatal(err)
	}
	first, err := loop.Run(context.Background(), "wykonaj pierwsza prace")
	if err != nil {
		t.Fatal(err)
	}
	drainEvents(t, first)
	second, err := loop.Run(context.Background(), "kolejne pytanie")
	if err != nil {
		t.Fatal(err)
	}
	drainEvents(t, second)

	if len(provider.reqs) != 3 {
		t.Fatalf("provider requests=%d, want tool/final/follow-up", len(provider.reqs))
	}
	if !requestContainsToolProtocol(provider.reqs[1]) {
		t.Fatal("live tool result was removed before the model could summarize it")
	}
	if requestContainsToolProtocol(provider.reqs[2]) {
		t.Fatalf("resolved tool protocol returned in follow-up prompt: %+v", provider.reqs[2])
	}
	if !requestContainsToolProtocol(loop.Messages) {
		t.Fatal("canonical transcript lost tool details needed by UI/search_history")
	}
}

func requestContainsToolProtocol(messages []llm.Message) bool {
	for _, message := range messages {
		if message.Role == llm.RoleTool || len(message.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

func TestHardProtocolCompactionIgnoresInternalToolImageUser(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "pierwsze prawdziwe pytanie"},
		{Role: llm.RoleAssistant, Content: "pierwsza odpowiedz"},
		{Role: llm.RoleUser, Content: "drugie prawdziwe pytanie"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "image-1", Name: "read_image", Arguments: `{}`}}},
		{Role: llm.RoleTool, ToolCallID: "image-1", Name: "read_image", Content: "image bytes externalized"},
		{Role: llm.RoleUser, Parts: []llm.ContentPart{
			{Type: llm.PartTypeText, Text: "Attached image from tool read_image:"},
			{Type: llm.PartTypeImage, Image: &llm.ImageRef{MediaType: "image/png", Data: "AA=="}},
		}},
		{Role: llm.RoleAssistant, Content: "druga odpowiedz"},
		{Role: llm.RoleUser, Content: "aktualne prawdziwe pytanie"},
	}
	if got := autoCompactSplit(messages); got != 3 {
		t.Fatalf("autoCompactSplit=%d, want second real user turn at 3", got)
	}
}
