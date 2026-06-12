package memory

import (
	"regexp"
	"strings"
)

// Reasoning tags produced by the providers in this repo:
//   - internal/llm/openai.go wraps delta.reasoning_content in
//     <thinking>...</thinking>
//   - internal/llm/codex.go wraps response.reasoning_*.delta the
//     same way
//   - some open models emit <think>...</think> or
//     <reasoning>...</reasoning> directly in content
//
// Streams can be interrupted, so an opening tag without a closing
// tag must also be handled (everything after it is reasoning).
var (
	reasoningBlockRe = regexp.MustCompile(`(?si)<(thinking|think|reasoning|reflection)>.*?</(thinking|think|reasoning|reflection)>`)
	reasoningOpenRe  = regexp.MustCompile(`(?si)<(thinking|think|reasoning|reflection)>.*$`)
)

// EstimateTokens exposes the package's crude len/4 token estimate
// for diagnostics (the /memory briefing line).
func EstimateTokens(s string) int { return estimateTokens(s) }

// StripReasoning removes model reasoning blocks from text so they
// never reach memory entries or summarization prompts. Closed
// blocks are removed wherever they appear; an unclosed opening tag
// drops everything from the tag to the end (truncated stream).
func StripReasoning(s string) string {
	low := strings.ToLower(s)
	if !strings.Contains(low, "<think") && !strings.Contains(low, "<reasoning") && !strings.Contains(low, "<reflection") {
		return s
	}
	s = reasoningBlockRe.ReplaceAllString(s, "")
	s = reasoningOpenRe.ReplaceAllString(s, "")
	// Collapse the blank lines left behind.
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}
