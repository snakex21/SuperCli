package agent

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// This is the original render, including exact separators and 700-byte tool
// excerpts. It deliberately remains independent of production assembly helpers.
func originalCompactTranscript(msgs []llm.Message) string {
	const toolResultCap = 700
	var b strings.Builder
	for _, m := range msgs {
		if m.Role == llm.RoleSystem {
			continue
		}
		content := m.Content
		if content == "" {
			for _, p := range m.Parts {
				if p.Type == llm.PartTypeText {
					content += p.Text
				}
			}
		}
		for _, tc := range m.ToolCalls {
			content += fmt.Sprintf("\n[tool call: %s %s]", tc.Name, compactExcerpt(tc.Arguments, toolResultCap))
		}
		if m.Role == llm.RoleTool {
			content = compactExcerpt(content, toolResultCap)
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		fmt.Fprintf(&b, "[%s] %s\n", m.Role, content)
	}
	return b.String()
}

func TestCompactTranscriptPreservesEveryByte(t *testing.T) {
	atoms := []string{"", " \t\n", "Dokładny kod źródłowy 🙂\u00a0", "\x00\xff", "\xc2", "\xa0", strings.Repeat("ę世\n", 350)}
	roles := []llm.Role{llm.RoleSystem, llm.RoleUser, llm.RoleAssistant, llm.RoleTool, "unknown"}
	rng := rand.New(rand.NewSource(77411))
	for n := 0; n < 2000; n++ {
		messages := make([]llm.Message, rng.Intn(12))
		for i := range messages {
			m := &messages[i]
			m.Role = roles[rng.Intn(len(roles))]
			if rng.Intn(2) == 0 {
				m.Content = atoms[rng.Intn(len(atoms))]
			}
			for p := 0; p < rng.Intn(16); p++ {
				part := llm.ContentPart{Type: llm.PartTypeText, Text: atoms[rng.Intn(len(atoms))]}
				if rng.Intn(3) == 0 {
					part.Type = llm.PartTypeReasoning
				}
				m.Parts = append(m.Parts, part)
			}
			for c := 0; c < rng.Intn(4); c++ {
				m.ToolCalls = append(m.ToolCalls, llm.ToolCall{Name: atoms[rng.Intn(len(atoms))], Arguments: atoms[rng.Intn(len(atoms))]})
			}
		}
		before, _ := json.Marshal(messages)
		want := originalCompactTranscript(messages)
		if got := RenderCompactTranscript(messages); got != want {
			t.Fatalf("production changed compaction bytes: got %q want %q", got, want)
		}
		after, _ := json.Marshal(messages)
		if string(before) != string(after) {
			t.Fatal("canonical history changed")
		}
	}
}

var compactTranscriptPerfSink string

func compactTranscriptPerfFixture(shape string) []llm.Message {
	messages := []llm.Message{{Role: llm.RoleSystem, Content: "Project instructions."}}
	for turn := 0; turn < 40; turn++ {
		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: "Preserve API compatibility, fix the reported failure, and run affected tests."})
		m := llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("Source inspection: known function and observed behavior.\n", 80)}
		if shape == "fragmented-coding" {
			m.Content = ""
			for i := 0; i < 64; i++ {
				m.Parts = append(m.Parts, llm.ContentPart{Type: llm.PartTypeText, Text: strings.Repeat("\tif err != nil { return fmt.Errorf(\"source: %w\", err) }\n", 4)})
			}
		}
		if shape == "tool-heavy-coding" {
			m.ToolCalls = []llm.ToolCall{{ID: fmt.Sprintf("read_%d", turn), Name: "read_many", Arguments: strings.Repeat(`{"file":"internal/service.go","from":1,"to":800}`, 40)}}
		}
		messages = append(messages, m)
		if shape == "tool-heavy-coding" {
			messages = append(messages, llm.Message{Role: llm.RoleTool, ToolCallID: m.ToolCalls[0].ID, Content: "== internal/service.go ==\n" + strings.Repeat("source body\n", 4000) + "\nCompleted: ok=1 failed=0"})
		}
	}
	return messages
}

func BenchmarkCompactTranscriptAssembly(b *testing.B) {
	for _, shape := range []string{"plain-coding", "fragmented-coding", "tool-heavy-coding"} {
		messages := compactTranscriptPerfFixture(shape)
		for _, implementation := range []struct {
			name string
			fn   func([]llm.Message) string
		}{{"original", originalCompactTranscript}, {"production", RenderCompactTranscript}} {
			b.Run(shape+"/"+implementation.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					compactTranscriptPerfSink = implementation.fn(messages)
				}
			})
		}
	}
}
