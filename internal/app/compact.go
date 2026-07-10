// /compact summarization: thin delegation to the shared
// implementation in internal/agent (agent.SummarizeForCompaction et
// al.), so the TUI, batch mode and the web GUI all compact the same
// way. Kept as package-local names because app has many call sites
// and tests that predate the move.
package app

import (
	"context"

	"supercli/internal/agent"
	"supercli/internal/llm"
)

// wrapCompactSummary wraps the model-produced summary in the
// resume framing that replaces the compacted messages.
func wrapCompactSummary(summary string) string {
	return agent.WrapCompactSummary(summary)
}

// summarizeForCompaction asks the provider for the Goal/Done/State/
// Pending summary of msgs. Returns an error when the provider fails
// or produces an empty answer.
func summarizeForCompaction(ctx context.Context, provider llm.Provider, msgs []llm.Message) (string, error) {
	return agent.SummarizeForCompaction(ctx, provider, msgs)
}

// compactFacts derives cheap EXACT context lines from the compacted
// messages: files read/modified and tools loaded via tool_search.
func compactFacts(msgs []llm.Message, loadedTools []string) string {
	return agent.CompactFacts(msgs, loadedTools)
}

// renderCompactTranscript renders the conversation as plain text for
// summarization (memory autosave shares it).
func renderCompactTranscript(msgs []llm.Message) string {
	return agent.RenderCompactTranscript(msgs)
}
