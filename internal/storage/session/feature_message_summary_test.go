package session

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"supercli/internal/llm"
)

func TestReadMessageSummaryMatchesTranscriptAndUpdates(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	chat, err := store.Create(t.TempDir(), "fixture", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	rows := []Encoded{
		{Role: "system", Content: "project rules"},
		{Role: "user", Content: "Unicode żółw 中文 😀"},
		{Role: "assistant", PartsJSON: "[{\"Type\":\"text\",\"Text\":\"answer\"}]"},
		{Role: "assistant", ToolCallsJSON: "[{\"ID\":\"a\",\"Name\":\"read\",\"Arguments\":\"{}\"}]"},
		{Role: "tool", ToolCallID: "a", Content: "numbered file output"},
		{Role: "assistant", PartsJSON: "["},
		{Role: "assistant", Content: "invalid call", ToolCallsJSON: "[{\"Name\":\"read\"}]"},
		{Role: "user", Content: ""},
		{Role: "user", Content: "\x00"},
		{Role: "assistant", PartsJSON: "[{\"Type\":\"reasoning\",\"Reasoning\":{\"Text\":\"scratch\"}},{\"Type\":\"text\",\"Text\":\"final\"}]"},
		{Role: "user", PartsJSON: "[{\"Type\":\"image\",\"Image\":{\"URL\":\"https://invalid.test/image\"}}]"},
	}
	assertParity := func(id string) {
		t.Helper()
		encoded, err := store.ReadMessages(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var messages []llm.Message
		var want MessageSummary
		for _, row := range encoded {
			message, err := row.ToMessage()
			if err != nil {
				continue
			}
			messages = append(messages, message)
			switch message.Role {
			case llm.RoleUser:
				want.Counts.User++
			case llm.RoleAssistant:
				want.Counts.Assistant++
				want.Counts.ToolCalls += len(message.ToolCalls)
			case llm.RoleTool:
				want.Counts.Tool++
			}
		}
		want.Breakdown = llm.EstimateRequestBreakdown(messages, nil)
		got, err := store.ReadMessageSummary(ctx, id)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("summary=%+v want=%+v err=%v", got, want, err)
		}
	}
	assertParity(chat.ID)
	assertParity("missing")
	for _, row := range rows {
		if err := store.AppendMessage(ctx, chat.ID, row); err != nil {
			t.Fatal(err)
		}
	}
	assertParity(chat.ID)
	if _, err := store.TruncateFrom(ctx, chat.ID, 5); err != nil {
		t.Fatal(err)
	}
	assertParity(chat.ID)
	other, err := store.Create(t.TempDir(), "fixture", "other")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(ctx, other.ID, Encoded{Role: "user", Content: "foreign"}); err != nil {
		t.Fatal(err)
	}
	assertParity(chat.ID)
	assertParity(other.ID)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.ReadMessageSummary(canceled, chat.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}
