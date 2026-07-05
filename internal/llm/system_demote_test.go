package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDemote_NoMidConversationSystem_Untouched: the common fast path —
// leading system block only — must return the slice unchanged (same
// backing array, zero rewrites).
func TestDemote_NoMidConversationSystem_Untouched(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "sys a"},
		{Role: RoleSystem, Content: "sys b"},
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "hello"},
	}
	out := demoteMidConversationSystemMessages(msgs)
	if len(out) != len(msgs) {
		t.Fatalf("len = %d, want %d", len(out), len(msgs))
	}
	for i := range msgs {
		if out[i].Role != msgs[i].Role || out[i].Content != msgs[i].Content {
			t.Fatalf("message %d changed: %+v", i, out[i])
		}
	}
}

// TestDemote_TailSystemNotHoisted locks in the cache fix at the unit
// level: a trailing system message (the per-request freshness stamp)
// stays at the TAIL as a <system-reminder> user turn; the leading
// system block keeps only the original leading run.
func TestDemote_TailSystemNotHoisted(t *testing.T) {
	out := demoteMidConversationSystemMessages([]Message{
		{Role: RoleSystem, Content: "base"},
		{Role: RoleUser, Content: "task"},
		{Role: RoleAssistant, Content: "on it"},
		{Role: RoleSystem, Content: "stamp 12:51"},
	})
	if len(out) != 4 {
		t.Fatalf("len = %d, want 4: %+v", len(out), out)
	}
	if out[0].Role != RoleSystem || out[0].Content != "base" {
		t.Fatalf("leading system changed: %+v", out[0])
	}
	last := out[len(out)-1]
	if last.Role != RoleUser {
		t.Fatalf("tail role = %q, want user", last.Role)
	}
	if last.Content != "<system-reminder>\nstamp 12:51\n</system-reminder>" {
		t.Fatalf("tail content = %q", last.Content)
	}
	for i := 1; i < len(out); i++ {
		if out[i].Role == RoleSystem {
			t.Fatalf("system message survived at index %d", i)
		}
	}
}

// TestDemote_MergesIntoPrecedingUser: system right after a plain user
// turn is appended to it (append-only at the tail), never emitted as a
// second consecutive user message.
func TestDemote_MergesIntoPrecedingUser(t *testing.T) {
	out := demoteMidConversationSystemMessages([]Message{
		{Role: RoleSystem, Content: "base"},
		{Role: RoleUser, Content: "task"},
		{Role: RoleSystem, Content: "preamble"},
		{Role: RoleSystem, Content: "stamp"},
	})
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(out), out)
	}
	want := "task\n\n<system-reminder>\npreamble\n</system-reminder>\n\n<system-reminder>\nstamp\n</system-reminder>"
	if out[1].Role != RoleUser || out[1].Content != want {
		t.Fatalf("merged user = %q, want %q", out[1].Content, want)
	}
	if !strings.HasPrefix(out[1].Content, "task") {
		t.Fatal("merge must append after the original user text (append-only tail)")
	}
}

// TestDemote_MergesFollowingUser: a user message directly after a
// demoted reminder (e.g. new user turn after a persisted reflection
// checkpoint) is folded into it — no user,user pair for strict
// alternating chat templates.
func TestDemote_MergesFollowingUser(t *testing.T) {
	out := demoteMidConversationSystemMessages([]Message{
		{Role: RoleSystem, Content: "base"},
		{Role: RoleUser, Content: "task"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "1", Name: "read_file", Arguments: `{}`}}},
		{Role: RoleTool, ToolCallID: "1", Content: "data"},
		{Role: RoleSystem, Content: "[reflection checkpoint @ step 8] keep going"},
		{Role: RoleUser, Content: "next task"},
	})
	if len(out) != 5 {
		t.Fatalf("len = %d, want 5: %+v", len(out), out)
	}
	merged := out[4]
	if merged.Role != RoleUser {
		t.Fatalf("role = %q, want user", merged.Role)
	}
	want := "<system-reminder>\n[reflection checkpoint @ step 8] keep going\n</system-reminder>\n\nnext task"
	if merged.Content != want {
		t.Fatalf("merged = %q, want %q", merged.Content, want)
	}
	// Tool pairing untouched.
	if out[2].Role != RoleAssistant || out[3].Role != RoleTool || out[3].ToolCallID != "1" {
		t.Fatalf("tool call/result pairing changed: %+v", out)
	}
}

// TestDemote_DoesNotMergeIntoMultimodalUser: user messages carrying
// image parts are left alone; the reminder becomes its own message.
func TestDemote_DoesNotMergeIntoMultimodalUser(t *testing.T) {
	out := demoteMidConversationSystemMessages([]Message{
		{Role: RoleSystem, Content: "base"},
		{Role: RoleUser, Parts: []ContentPart{
			{Type: PartTypeText, Text: "what is this?"},
			{Type: PartTypeImage, Image: &ImageRef{Data: "AAAA", MediaType: "image/png"}},
		}},
		{Role: RoleSystem, Content: "stamp"},
	})
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3: %+v", len(out), out)
	}
	if len(out[1].Parts) != 2 {
		t.Fatalf("multimodal user mutated: %+v", out[1])
	}
	if out[2].Role != RoleUser || !strings.Contains(out[2].Content, "stamp") {
		t.Fatalf("standalone reminder wrong: %+v", out[2])
	}
}

// TestBuildAnthropicRequest_TailSystemNotHoisted covers the Anthropic
// path of the same cache killer: buildAnthropicRequest used to join
// ALL system messages into req.System (prompt front). Now only the
// leading run may be hoisted; the trailing stamp must arrive as the
// last user message wrapped in <system-reminder>.
func TestBuildAnthropicRequest_TailSystemNotHoisted(t *testing.T) {
	body, err := buildAnthropicRequest("claude-sonnet-4-5", []Message{
		{Role: RoleSystem, Content: "base system"},
		{Role: RoleUser, Content: "task"},
		{Role: RoleAssistant, Content: "on it"},
		{Role: RoleSystem, Content: "Current local date/time: 2026-07-05 12:51"},
	}, nil, false, 4096)
	if err != nil {
		t.Fatal(err)
	}
	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if req.System != "base system" {
		t.Fatalf("req.System = %q, want only the leading system block (stamp must not be hoisted)", req.System)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("messages = %d, want 3: %+v", len(req.Messages), req.Messages)
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != "user" {
		t.Fatalf("last role = %q, want user", last.Role)
	}
	if len(last.Content) != 1 || last.Content[0].Type != "text" ||
		!strings.Contains(last.Content[0].Text, "<system-reminder>") ||
		!strings.Contains(last.Content[0].Text, "12:51") {
		t.Fatalf("last message is not the demoted stamp: %+v", last.Content)
	}
}
