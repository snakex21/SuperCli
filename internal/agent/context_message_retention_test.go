package agent

import (
	"encoding/json"
	"reflect"
	"testing"

	"supercli/internal/llm"
)

func retentionHistoryFixture() []llm.Message {
	image := &llm.ImageRef{Path: "current.png", MediaType: "image/png", ID: "current-image", Active: true}
	native := &llm.ReasoningBlock{Format: llm.ReasoningResponses, Model: "fixture", Scope: "fixture-origin", Data: json.RawMessage(`{"type":"reasoning","encrypted_content":"opaque continuation"}`)}
	return []llm.Message{
		{Role: llm.RoleSystem, Content: "standing instructions"},
		{Role: llm.RoleSystem, Content: "project policy"},
		{Role: llm.RoleUser, Content: "previous task"},
		{Role: llm.RoleAssistant, Content: "previous result"},
		{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "current task"}, {Type: llm.PartTypeImage, Image: image}}},
		{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeReasoning, Reasoning: native}}, ToolCalls: []llm.ToolCall{{ID: "current-call", Name: "execute", Arguments: `{"command":"test current"}`}}},
		{Role: llm.RoleTool, Name: "execute", ToolCallID: "current-call", Content: "command_failed exit=1: current failure must remain visible"},
	}
}

func retentionArchiveJSON(t *testing.T, messages []llm.Message) string {
	t.Helper()
	data, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCompactPrefixReleasesOldArrayWithoutChangingArchive(t *testing.T) {
	history := retentionHistoryFixture()
	loop := &Loop{Messages: history, hidden: []bool{false, true, false, true, false, false, true}}
	archived := retentionArchiveJSON(t, history)
	want := append(append([]llm.Message(nil), history[:2]...), llm.Message{Role: llm.RoleUser, Content: "verified summary"})
	want = append(want, history[4:]...)
	if removed := loop.CompactPrefixWithSummary("verified summary", 4); removed != 2 {
		t.Fatalf("removed %d messages, want 2", removed)
	}
	if !reflect.DeepEqual(loop.Messages, want) {
		t.Fatalf("retained message chronology or protocol payload changed: %#v", loop.Messages)
	}
	if got := retentionArchiveJSON(t, history); got != archived {
		t.Fatal("compaction mutated an external archive/history view")
	}
	if cap(loop.Messages) != len(loop.Messages) {
		t.Fatalf("compacted array retains unused message slots: len=%d cap=%d", len(loop.Messages), cap(loop.Messages))
	}
	if &loop.Messages[0] == &history[0] {
		t.Fatal("compaction still owns the discarded history backing array")
	}
	if !reflect.DeepEqual(loop.hidden, []bool{false, true, false, false, false, true}) {
		t.Fatalf("hidden indices not remapped: %v", loop.hidden)
	}
}

func TestLoadConversationReleasesOldArrayWithAliasedInput(t *testing.T) {
	history := retentionHistoryFixture()
	history[5].Content = "<thinking>old display reasoning</thinking>continue current tool"
	loop := &Loop{Messages: history, hidden: []bool{false, false, true}, chatWindowStart: 3}
	archived := retentionArchiveJSON(t, history)
	want := append([]llm.Message(nil), history[:2]...)
	want = append(want, history[4:]...)
	want[3].Content = "continue current tool"
	loop.LoadConversation(history[4:])
	if !reflect.DeepEqual(loop.Messages, want) {
		t.Fatalf("resume changed current image, native state, or call/result pair: %#v", loop.Messages)
	}
	if got := retentionArchiveJSON(t, history); got != archived {
		t.Fatal("resume mutated the previously loaded archive/history view")
	}
	if cap(loop.Messages) != len(loop.Messages) {
		t.Fatalf("resumed array retains unused message slots: len=%d cap=%d", len(loop.Messages), cap(loop.Messages))
	}
	if &loop.Messages[0] == &history[0] {
		t.Fatal("resume still owns the replaced history backing array")
	}
	if loop.HiddenCount() != 0 || loop.chatWindowStart != 0 {
		t.Fatal("resume did not reset visibility/window state")
	}
}

func TestLoadConversationEmptyReleasesOldCapacity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages []llm.Message
		wantNil  bool
	}{
		{name: "nil", wantNil: true},
		{name: "empty with spare capacity", messages: make([]llm.Message, 0, 64)},
		{name: "replaced body", messages: []llm.Message{{Role: llm.RoleUser, Content: "old body"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loop := &Loop{Messages: tc.messages}
			loop.LoadConversation(nil)
			if len(loop.Messages) != 0 || cap(loop.Messages) != 0 {
				t.Fatalf("empty resumed history retains capacity: len=%d cap=%d", len(loop.Messages), cap(loop.Messages))
			}
			if (loop.Messages == nil) != tc.wantNil {
				t.Fatal("nil/empty history shape changed")
			}
		})
	}
}

func TestCompactPrefixNoReplacementKeepsArray(t *testing.T) {
	history := retentionHistoryFixture()
	loop := &Loop{Messages: history}
	if removed := loop.CompactPrefixWithSummary("not used", 2); removed != 0 {
		t.Fatalf("removed %d messages for a no-op", removed)
	}
	if &loop.Messages[0] != &history[0] || len(loop.Messages) != len(history) {
		t.Fatal("no-op compaction unnecessarily rebuilt the history")
	}
}
