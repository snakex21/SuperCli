package agent

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// Preserve the pre-optimization rendering as an independent byte oracle.
func originalDraftText(m llm.Message) string {
	var sb strings.Builder
	if m.Content != "" {
		sb.WriteString(m.Content)
	}
	for _, p := range m.Parts {
		if p.Type == llm.PartTypeText && p.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(p.Text)
		}
	}
	if len(m.ToolCalls) > 0 {
		var names []string
		for _, tc := range m.ToolCalls {
			names = append(names, tc.Name)
		}
		if sb.Len() > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString("[called tools: " + strings.Join(names, ", ") + "]")
	}
	return strings.TrimSpace(sb.String())
}

func TestDraftTextPreparationCompatibility(t *testing.T) {
	atoms := []llm.ContentPart{
		{Type: llm.PartTypeText},
		{Type: llm.PartTypeText, Text: " \t\n"},
		{Type: llm.PartTypeText, Text: " źródło 🙂\u00a0"},
		{Type: llm.PartTypeText, Text: "\x00\xff"},
		{Type: llm.PartTypeReasoning, Text: "private"},
		{Type: llm.PartTypeImage, Text: "ignored"},
		{Type: "unknown", Text: "ignored"},
	}
	rng := rand.New(rand.NewSource(77531))
	for n := 0; n < 2000; n++ {
		m := llm.Message{Role: llm.RoleSystem, Content: atoms[rng.Intn(4)].Text}
		m.Parts = make([]llm.ContentPart, rng.Intn(20))
		for i := range m.Parts {
			m.Parts[i] = atoms[rng.Intn(len(atoms))]
		}
		m.ToolCalls = make([]llm.ToolCall, rng.Intn(6))
		for i := range m.ToolCalls {
			m.ToolCalls[i].Name = atoms[rng.Intn(4)].Text
		}
		before, _ := json.Marshal(m)
		want := originalDraftText(m)
		if got := messageDraftText(m); got != want {
			t.Fatalf("production changed rendered bytes: got %q want %q", got, want)
		}
		after, _ := json.Marshal(m)
		if string(before) != string(after) {
			t.Fatal("canonical message modified")
		}
	}
}

var draftTextPerfSink string

func BenchmarkDraftTextPreparation(b *testing.B) {
	for _, shape := range []string{"plain-instructions", "single-text", "fragmented", "tool-batch"} {
		m := llm.Message{Role: llm.RoleSystem, Content: strings.Repeat("Use project conventions; keep changes scoped and verify affected behavior.\n", 320)}
		switch shape {
		case "single-text":
			m.Parts = []llm.ContentPart{{Type: llm.PartTypeText, Text: m.Content}}
			m.Content = ""
		case "fragmented":
			for i := 0; i < 16; i++ {
				m.Parts = append(m.Parts, llm.ContentPart{Type: llm.PartTypeText, Text: strings.Repeat("Convention; preserve behavior.\n", 48)})
			}
			m.Content = ""
		case "tool-batch":
			m.Role = llm.RoleAssistant
			for i := 0; i < 8; i++ {
				m.ToolCalls = append(m.ToolCalls, llm.ToolCall{Name: fmt.Sprintf("inspect_module_%d", i)})
			}
		}
		for _, implementation := range []struct {
			name string
			fn   func(llm.Message) string
		}{{"original", originalDraftText}, {"production", messageDraftText}} {
			b.Run(shape+"/"+implementation.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					draftTextPerfSink = implementation.fn(m)
				}
			})
		}
	}
}

func BenchmarkCodingInstructionPreparation(b *testing.B) {
	l := contextPreparationFixture(b, true)
	l.Messages[0].Content = strings.Repeat("Use project conventions; keep changes scoped and verify affected behavior.\n", 320)
	for i := range l.Messages {
		if l.Messages[i].Role == llm.RoleAssistant {
			l.Messages[i].Content = strings.Repeat("\tif err != nil { return fmt.Errorf(\"source: %w\", err) }\r\n", 160)
		}
	}
	l.invalidateVisibleEstimate()
	l.EstimateNextRequestTokens()
	b.ReportAllocs()
	for b.Loop() {
		l.EstimateNextRequestTokens()
		l.EstimateNextRequestTokens()
		messages, tokens := l.prepareProviderMessages(true)
		preparedRequestMessagesSink = messages
		preparedRequestEstimateSink = tokens + estimateRequestTokens(nil, l.buildToolDefs())
	}
}
