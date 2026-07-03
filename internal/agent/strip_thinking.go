package agent

import (
	"regexp"
	"strings"

	"supercli/internal/llm"
)

// Reasoning blocks the providers emit into assistant Content:
//   - internal/llm/openai.go and codex.go wrap streamed
//     reasoning_content in <thinking>...</thinking>
//   - some open models emit <think>, <reasoning> or <reflection> directly
//
// Per the Qwen/DeepSeek convention, chain-of-thought from PRIOR turns is
// stripped from the history sent back to the model: only the final
// answer is context. We strip at the moment the assistant message enters
// l.Messages (deterministically, once), keeping the stable prefix intact
// and never rewriting already-sent bytes in flight. The full text
// (with thinking) is still persisted to the session store so the UI can
// replay it.
var (
	thinkBlockRe = regexp.MustCompile(`(?si)<(thinking|think|reasoning|reflection)>.*?</(thinking|think|reasoning|reflection)>`)
	thinkOpenRe  = regexp.MustCompile(`(?si)<(thinking|think|reasoning|reflection)>.*$`)
)

// stripThinking removes reasoning blocks from a single string. Closed
// blocks are removed wherever they appear; an unclosed opening tag drops
// everything from the tag onward (truncated stream). Returns the input
// unchanged when no reasoning marker is present (fast path).
func stripThinking(s string) string {
	low := strings.ToLower(s)
	if !strings.Contains(low, "<think") && !strings.Contains(low, "<reasoning") && !strings.Contains(low, "<reflection") {
		return s
	}
	s = thinkBlockRe.ReplaceAllString(s, "")
	s = thinkOpenRe.ReplaceAllString(s, "")
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}

// stripThinkingFromMessage returns a copy of msg with reasoning blocks
// removed from its text (Content and any text Parts). Non-text parts and
// all other fields (role, tool calls) are preserved. Only assistant
// messages carry reasoning, so callers should gate on role; this
// function is safe to call on any message regardless.
func stripThinkingFromMessage(msg llm.Message) llm.Message {
	if msg.Content != "" {
		msg.Content = stripThinking(msg.Content)
	}
	if len(msg.Parts) > 0 {
		parts := make([]llm.ContentPart, len(msg.Parts))
		copy(parts, msg.Parts)
		for i := range parts {
			if parts[i].Type == llm.PartTypeText {
				parts[i].Text = stripThinking(parts[i].Text)
			}
		}
		msg.Parts = parts
	}
	return msg
}
