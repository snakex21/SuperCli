package agent

import (
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// Long coding session: verbose user briefs, long assistant reports, tool
// calls and large tool results across many completed turns.
func compactLongHistoryFixture() []llm.Message {
	messages := []llm.Message{{Role: llm.RoleSystem, Content: "Project instructions."}}
	for turn := 0; turn < 60; turn++ {
		user := fmt.Sprintf("Turn %d: keep API compatibility and run the affected tests.\n", turn) +
			strings.Repeat("Context pasted by the user: stack trace line and log excerpt.\n", 20+turn%5*30)
		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: user})
		call := llm.ToolCall{ID: fmt.Sprintf("read_%d", turn), Name: "read_many", Arguments: `{"reads":"internal/service.go:1-300"}`}
		messages = append(messages,
			llm.Message{Role: llm.RoleAssistant, Content: "Inspecting the service.", ToolCalls: []llm.ToolCall{call}},
			llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: strings.Repeat("source body\n", 2000) + "Completed: ok=1"},
			llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("Report: changed file, reason and verification result.\n", 40+turn%4*60)},
		)
	}
	return messages
}

func BenchmarkCompactSummaryInput(b *testing.B) {
	messages := compactLongHistoryFixture()
	for _, implementation := range []struct {
		name string
		fn   func([]llm.Message) string
	}{{"full-render", RenderCompactTranscript}, {"summary-input", compactSummaryInput}} {
		b.Run(implementation.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				compactTranscriptPerfSink = implementation.fn(messages)
			}
			b.ReportMetric(float64(len(compactTranscriptPerfSink)), "transcript-bytes")
		})
	}
}
