package reflect

import (
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/llm"
)

func TestModelReflectorTranscriptIncludesPartsAndToolEvidence(t *testing.T) {
	r := &ModelReflector{HistoryTail: 5}
	r.Prepare()
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "stale fallback", Parts: []llm.ContentPart{
			{Type: llm.PartTypeText, Text: "Fix the failing "},
			{Type: llm.PartTypeText, Text: "test\nwithout changing its assertion"},
			{Type: llm.PartTypeImage, Image: &llm.ImageRef{Data: "private-image-data", Path: "/private/image.png"}},
		}},
		{Role: llm.RoleAssistant, Parts: []llm.ContentPart{
			{Type: llm.PartTypeReasoning, Text: "private reasoning", Reasoning: &llm.ReasoningBlock{Data: []byte(`{"secret":"private-signed-reasoning"}`)}},
			{Type: llm.PartTypeText, Text: "Checking the fix"},
		}, ToolCalls: []llm.ToolCall{{ID: "check-1", Name: "shell", Arguments: `{"command":"go test ./..."}`}}},
		{Role: llm.RoleTool, Name: "shell", ToolCallID: "check-1", Content: "FAIL: expected 5, got 6"},
	}
	got := r.transcript(history)
	for _, want := range []string{"Fix the failing test without changing its assertion", "Checking the fix", "shell", "check-1", "FAIL: expected 5, got 6"} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q: %s", want, got)
		}
	}
	for _, forbidden := range []string{"stale fallback", "private reasoning", "private-signed-reasoning", "private-image-data", "/private/image.png", `{"command":"go test ./..."}`} {
		if strings.Contains(got, forbidden) {
			t.Errorf("transcript exposed %q: %s", forbidden, got)
		}
	}
}

func TestModelReflectorTranscriptSystemMessagesDoNotConsumeTail(t *testing.T) {
	r := &ModelReflector{HistoryTail: 2}
	r.Prepare()
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "old request"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-2", Name: "shell", Arguments: `{"command":"go test"}`}}},
		{Role: llm.RoleSystem, Content: "old reflection"},
		{Role: llm.RoleTool, Name: "shell", ToolCallID: "call-2", Content: "test failed"},
		{Role: llm.RoleSystem, Content: "verification reminder"},
		{Role: llm.RoleSystem, Content: "goal reminder"},
	}
	got := r.transcript(history)
	for _, want := range []string{"shell", "test failed"} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q: %s", want, got)
		}
	}
	for _, forbidden := range []string{"old request", "old reflection", "verification reminder", "goal reminder"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("transcript retained %q: %s", forbidden, got)
		}
	}
}

func TestModelReflectorTranscriptBoundsEachMessage(t *testing.T) {
	r := &ModelReflector{HistoryTail: 2}
	r.Prepare()
	got := r.transcript([]llm.Message{
		{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: strings.Repeat("ą", 5000)}}},
		{Role: llm.RoleTool, Name: "shell", ToolCallID: "check", Content: "current failure"},
	})
	if len(got) > 2200 || !strings.Contains(got, "[truncated]") || !strings.Contains(got, "current failure") {
		t.Fatalf("unbounded or missing recent evidence: bytes=%d, tail=%q", len(got), got[max(0, len(got)-60):])
	}
	if !utf8.ValidString(got) {
		t.Fatal("truncation split a UTF-8 character")
	}
}

func TestTranscriptLineUTF8Boundaries(t *testing.T) {
	for _, suffix := range []string{"", "x", "ą", "🙂"} {
		t.Run(suffix, func(t *testing.T) {
			var line transcriptLine
			line.write(strings.Repeat("x", reflectionTranscriptMessageBytes-1))
			line.write(suffix)
			if line.b.Len() > reflectionTranscriptMessageBytes || !utf8.ValidString(line.b.String()) {
				t.Fatalf("invalid bounded line: length=%d", line.b.Len())
			}
			if line.truncated != (len(suffix) > 1) {
				t.Fatalf("truncated=%v for suffix %q", line.truncated, suffix)
			}
		})
	}
}

func TestModelReflectorTranscriptEmptyAndPrivateParts(t *testing.T) {
	r := &ModelReflector{HistoryTail: 2}
	r.Prepare()
	if got := r.transcript(nil); got != "" {
		t.Fatalf("nil transcript=%q", got)
	}
	if got := r.transcript([]llm.Message{{Role: llm.RoleSystem, Content: "hidden"}}); got != "" {
		t.Fatalf("system transcript=%q", got)
	}
	got := r.transcript([]llm.Message{{Role: llm.RoleAssistant, Content: "stale", Parts: []llm.ContentPart{{Type: llm.PartTypeReasoning, Text: "hidden"}}}})
	if got != "[assistant]" {
		t.Fatalf("private-only parts leaked fallback: %q", got)
	}
}
