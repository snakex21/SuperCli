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
		// Strip text parts and drop any that become empty. A turn whose
		// only text was a <thinking> block (the model reasoned, then
		// emitted a tool call with no visible answer) would otherwise
		// leave an empty text part, which the provider rejects on the
		// next request. Non-text parts (images) are always kept.
		parts := make([]llm.ContentPart, 0, len(msg.Parts))
		for _, p := range msg.Parts {
			if p.Type == llm.PartTypeText {
				p.Text = stripThinking(p.Text)
				if p.Text == "" {
					continue
				}
			}
			parts = append(parts, p)
		}
		msg.Parts = parts
	}
	return msg
}
