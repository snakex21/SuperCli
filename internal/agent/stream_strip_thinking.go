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
	plain, _ := captureThinking(s)
	return plain
}

// captureThinking is the single-string core of the strip: it removes
// reasoning blocks and returns (plain, captured). captured keeps the
// original tags — closed blocks verbatim, an unclosed opening tag
// captures the truncated remainder — so the loop can hand the
// reasoning back to the model on the next request instead of throwing
// it away (SUPERCLI_KEEP_THINKING).
func captureThinking(s string) (plain, captured string) {
	low := strings.ToLower(s)
	if !strings.Contains(low, "<think") && !strings.Contains(low, "<reasoning") && !strings.Contains(low, "<reflection") {
		return s, ""
	}
	var kept, taken strings.Builder
	last := 0
	for _, m := range thinkBlockRe.FindAllStringSubmatchIndex(s, -1) {
		kept.WriteString(s[last:m[0]])
		taken.WriteString(s[m[0]:m[1]])
		taken.WriteByte('\n')
		last = m[1]
	}
	kept.WriteString(s[last:])
	plain = kept.String()
	// Unclosed opening tag (truncated stream): capture from the tag on.
	if open := thinkOpenRe.FindStringIndex(plain); open != nil {
		taken.WriteString(plain[open[0]:])
		plain = plain[:open[0]]
	}
	for strings.Contains(plain, "\n\n\n") {
		plain = strings.ReplaceAll(plain, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(plain), strings.TrimSpace(taken.String())
}

// stripThinkingFromMessage returns a copy of msg with reasoning blocks
// removed from its text (Content and any text Parts). Non-text parts and
// all other fields (role, tool calls) are preserved. Only assistant
// messages carry reasoning, so callers should gate on role; this
// function is safe to call on any message regardless.
func stripThinkingFromMessage(msg llm.Message) llm.Message {
	_, plain := captureThinkingFromMessage(msg)
	return plain
}

// captureThinkingFromMessage is stripThinkingFromMessage plus the
// captured reasoning: it returns the stripped blocks (concatenated
// across Content and text Parts, in order) and the plain provider-
// facing copy. The loop stores the captured text on l.lastThinking so
// the next request can continue from the previous chain of thought.
func captureThinkingFromMessage(msg llm.Message) (string, llm.Message) {
	var buf strings.Builder
	strip := func(s string) string {
		plain, captured := captureThinking(s)
		buf.WriteString(captured)
		return plain
	}
	if msg.Content != "" {
		msg.Content = strip(msg.Content)
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
				p.Text = strip(p.Text)
				if p.Text == "" {
					continue
				}
			}
			parts = append(parts, p)
		}
		msg.Parts = parts
	}
	// Some local reasoning models occasionally finish a turn with only a
	// reasoning channel and no visible answer. Stripping that reasoning used
	// to leave an invalid empty assistant message in history; the NEXT request
	// then failed before reaching the model. Keep a tiny semantic placeholder
	// only in provider-facing history. The persisted/UI copy is written before
	// this function runs and therefore retains the original reasoning stream.
	if msg.Role == llm.RoleAssistant && msg.Content == "" && len(msg.Parts) == 0 && len(msg.ToolCalls) == 0 {
		msg.Content = "[no visible answer]"
	}
	return buf.String(), msg
}
