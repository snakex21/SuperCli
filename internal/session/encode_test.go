package session

import (
	"testing"

	"supercli/internal/llm"
)

func TestFromMessage_Basic(t *testing.T) {
	m := llm.Message{Role: llm.RoleUser, Content: "hello"}
	enc, err := FromMessage(m)
	if err != nil {
		t.Fatalf("FromMessage: %v", err)
	}
	if enc.Role != "user" {
		t.Errorf("Role = %q", enc.Role)
	}
	if enc.Content != "hello" {
		t.Errorf("Content = %q", enc.Content)
	}
}

func TestFromMessage_WithParts(t *testing.T) {
	m := llm.Message{
		Role: llm.RoleUser,
		Parts: []llm.ContentPart{
			{Type: llm.PartTypeText, Text: "what is this?"},
			{Type: llm.PartTypeImage, Image: &llm.ImageRef{URL: "http://x/y.png", MediaType: "image/png"}},
		},
	}
	enc, err := FromMessage(m)
	if err != nil {
		t.Fatalf("FromMessage: %v", err)
	}
	if enc.PartsJSON == "" {
		t.Error("PartsJSON empty")
	}
	if enc.Content != "" {
		t.Errorf("Content should be empty when Parts present, got %q", enc.Content)
	}
}

func TestFromMessage_WithToolCalls(t *testing.T) {
	m := llm.Message{
		Role:      llm.RoleAssistant,
		Content:   "",
		ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read_image", Arguments: `{"path":"/x"}`}},
	}
	enc, err := FromMessage(m)
	if err != nil {
		t.Fatalf("FromMessage: %v", err)
	}
	if enc.ToolCallsJSON == "" {
		t.Error("ToolCallsJSON empty")
	}
}

func TestFromMessage_RejectsInvalid(t *testing.T) {
	if _, err := FromMessage(llm.Message{}); err == nil {
		t.Fatal("expected error on empty message")
	}
}

func TestEncoded_ToMessage_Roundtrip(t *testing.T) {
	original := llm.Message{Role: llm.RoleUser, Content: "hello", Name: "alice"}
	enc, _ := FromMessage(original)
	back, err := enc.ToMessage()
	if err != nil {
		t.Fatalf("ToMessage: %v", err)
	}
	if back.Content != "hello" || back.Name != "alice" || back.Role != llm.RoleUser {
		t.Errorf("roundtrip mismatch: %+v", back)
	}
}

func TestEncoded_ToMessage_WithParts(t *testing.T) {
	original := llm.Message{
		Role: llm.RoleUser,
		Parts: []llm.ContentPart{
			{Type: llm.PartTypeText, Text: "describe this"},
			{Type: llm.PartTypeImage, Image: &llm.ImageRef{Data: "BASE64", MediaType: "image/jpeg"}},
		},
	}
	enc, _ := FromMessage(original)
	back, err := enc.ToMessage()
	if err != nil {
		t.Fatalf("ToMessage: %v", err)
	}
	if len(back.Parts) != 2 {
		t.Fatalf("parts len = %d", len(back.Parts))
	}
	if back.Parts[1].Image.Data != "BASE64" {
		t.Errorf("image data = %q", back.Parts[1].Image.Data)
	}
}

func TestEncoded_ToMessage_InvalidJSON(t *testing.T) {
	enc := Encoded{Role: "user", Content: "x", PartsJSON: "not json"}
	if _, err := enc.ToMessage(); err == nil {
		t.Fatal("expected error on invalid parts JSON")
	}
}

func TestEncoded_Validate(t *testing.T) {
	cases := []struct {
		name string
		enc  Encoded
		ok   bool
	}{
		{"user", Encoded{Role: "user", Content: "x"}, true},
		{"empty_role", Encoded{Content: "x"}, false},
		{"bad_role", Encoded{Role: "banana", Content: "x"}, false},
		{"tool_no_id", Encoded{Role: "tool", Content: "x"}, false},
		{"tool_with_id", Encoded{Role: "tool", ToolCallID: "c", Content: "x"}, true},
	}
	for _, c := range cases {
		err := c.enc.Validate()
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
}
