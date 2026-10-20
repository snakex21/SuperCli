package session

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/llm"
)

func TestHistoryExcerptMultipartAndBytePagination(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	chat, err := s.Create("fixture", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	visible := "Verified: zażółć 日本語 😀\nKeep all data beside the executable."
	msg := llm.Message{Role: llm.RoleAssistant, Parts: []llm.ContentPart{
		{Type: llm.PartTypeText, Text: visible},
		{Type: llm.PartTypeReasoning, Reasoning: &llm.ReasoningBlock{Format: llm.ReasoningChat, Model: "fixture", Scope: "fixture", Data: []byte(`{"reasoning_content":"PRIVATE-NATIVE-STATE"}`)}},
		{Type: llm.PartTypeImage, Image: &llm.ImageRef{Data: "PRIVATE-PIXELS", MediaType: "image/png"}},
	}}
	encoded, err := FromMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AppendMessage(ctx, chat.ID, encoded); err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	offset, pages := 0, 0
	for {
		h, err := s.ReadHistoryExcerpt(ctx, chat.ID, 1, offset, 7)
		if err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(h.Text) || len(h.Text) > 7 || h.TotalBytes != len(visible) || h.Offset != offset {
			t.Fatal("invalid or unbounded page")
		}
		got.WriteString(h.Text)
		pages++
		if h.NextOffset == 0 {
			break
		}
		if h.NextOffset <= offset || pages > len(visible) {
			t.Fatal("pagination did not progress")
		}
		offset = h.NextOffset
	}
	if got.String() != visible || strings.Contains(got.String(), "PRIVATE-") {
		t.Fatal("visible text changed or hidden data leaked")
	}
	if _, err := s.ReadHistoryExcerpt(ctx, "another-session", 1, 0, 32); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("read crossed session boundary")
	}
	if _, err := s.ReadHistoryExcerpt(ctx, chat.ID, 1, len(visible)+1, 32); err == nil {
		t.Fatal("invalid offset accepted")
	}
	if _, err := s.ReadHistoryExcerpt(ctx, chat.ID, 1, 0, 1); err == nil {
		t.Fatal("sub-rune page can fail to advance")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ReadHistoryExcerpt(ctx, chat.ID, 1, 0, 32); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
}

func TestHistoryExcerptIncludesExactToolCallAndResult(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	chat, err := s.Create("fixture", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	call := llm.ToolCall{ID: "checked", Name: "ctx_execute", Arguments: `{"command":"go test ./..."}`}
	for _, msg := range []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}, {Role: llm.RoleTool, ToolCallID: call.ID, Name: call.Name, Content: "exit_code=3: original failure"}} {
		row, err := FromMessage(msg)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.AppendMessage(ctx, chat.ID, row); err != nil {
			t.Fatal(err)
		}
	}
	h, err := s.ReadHistoryExcerpt(ctx, chat.ID, 1, 0, 8192)
	if err != nil || !strings.Contains(h.Text, call.Arguments) {
		t.Fatalf("tool intent unavailable: %v", err)
	}
	h, err = s.ReadHistoryExcerpt(ctx, chat.ID, 2, 0, 8192)
	if err != nil || h.Text != "exit_code=3: original failure" {
		t.Fatalf("tool evidence changed: %v", err)
	}
}

func TestHistoryExcerptLegacyContentWithNativeParts(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	chat, err := s.Create("fixture", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range []string{"", "Additional visible text."} {
		msg := llm.Message{Role: llm.RoleAssistant, Content: "Original visible answer.", Parts: []llm.ContentPart{{Type: llm.PartTypeReasoning, Reasoning: &llm.ReasoningBlock{Format: llm.ReasoningChat, Model: "fixture", Scope: "fixture", Data: []byte(`{"reasoning_content":"PRIVATE"}`)}}}}
		if extra != "" {
			msg.Parts = append(msg.Parts, llm.ContentPart{Type: llm.PartTypeText, Text: extra})
		}
		row, err := FromMessage(msg)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.AppendMessage(ctx, chat.ID, row); err != nil {
			t.Fatal(err)
		}
		seq := 1
		if extra != "" {
			seq = 2
		}
		h, err := s.ReadHistoryExcerpt(ctx, chat.ID, seq, 0, 8192)
		if err != nil || h.Text != msg.TextOnly().Content {
			t.Fatalf("legacy visible text changed: %v", err)
		}
	}
}
