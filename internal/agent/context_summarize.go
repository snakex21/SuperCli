// Compaction summarization shared by every front-end (TUI, batch,
// web GUI): turn the conversation into a single structured summary
// message instead of merely hiding old turns. Lives in the agent
// package so all loop owners wire the SAME summarizer — a front-end
// that forgets to wire one degrades to the blind hide fallback in
// window.go, which loses the whole prior conversation (the exact
// "session compacted itself and the AI has no idea what is going on"
// failure observed in webgui sessions before this was shared).
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"supercli/internal/llm"
)

// Keep the placeholders shared with the narrow empty-template echo check.
// Factoring this constant does not change the provider's instruction bytes.
const compactionTemplate = `Goal: user's requests and intent; quote the key instruction verbatim.
Done: completed files, fixes and checks supported by tool/task results.
State: needed paths, decisions, values, defaults, open errors and ALL still-applicable user requirements/prohibitions, including earlier ones. Keep constraints as instructions, not just past compliance.
Pending: unfinished work and immediate next step.`

// compactionPrompt is the shared summarization instruction. Exact file/tool
// facts are appended by code (CompactFacts); the model supplies task state.
const compactionPrompt = `Summarize the conversation to REPLACE older history and let work continue without it.

Use EXACTLY this plain-text template, no code fences:
` + compactionTemplate + `

Tool/task results are authoritative. Assistant intentions/promises are not Done.
A successful worker's returned investigation is not Pending. Preserve failed worker IDs; continue them with send_message when useful instead of restarting.

Hard limit: 500 tokens total. Prefer bare paths and short phrases. TEXT ONLY — no tool calls.`

var errCompactionInstructionEcho = errors.New("compact: model repeated summarization instructions instead of conversation facts; history preserved")

// compactSummaryMaxChars hard-caps the model-produced summary
// (~1000 tokens). Small models overshoot instruction limits; the
// summary is re-sent with EVERY later request, so an unbounded one
// would defeat the point of compacting. Truncation prefers a line
// boundary.
const compactSummaryMaxChars = 4000

// compactSummaryWrapper frames the summary so the model resumes
// seamlessly on the next turn.
const (
	compactSummaryPreamble = "Earlier context was compacted. Continue using this summary:\n\n"
	compactSummaryEpilogue = "\n\nResume the work; do not acknowledge this summary or ask the user to repeat information."
	// Older sessions may still contain the original envelope. Keep it
	// recognizable without rewriting archived conversation or summary facts.
	legacyCompactSummaryPreamble = "This session is continued from a previous conversation that was compacted to save context. The conversation is summarized below:\n\n"
	legacyCompactSummaryEpilogue = "\n\nPlease continue the conversation from where it was left off. Do not ask the user to repeat anything and do not acknowledge this summary in your response."
)

// WrapCompactSummary wraps the model-produced summary in the
// resume framing that replaces the compacted messages.
func WrapCompactSummary(summary string) string {
	return compactSummaryPreamble + strings.TrimSpace(summary) + compactSummaryEpilogue
}

// renderCompactTranscript renders the conversation as plain text
// for the summarizer. System messages are skipped (the summarizer
// gets its own instructions); tool results are truncated so a
// huge file read doesn't blow the summarization call itself.
func RenderCompactTranscript(msgs []llm.Message) string {
	if len(msgs) == 0 {
		return ""
	}
	// Render each message once, then reserve the exact transcript length.
	// Repeated append/format of large coding histories otherwise copies text
	// on every buffer growth. These are request-local strings, not a cache.
	contents := make([]string, len(msgs))
	size := 0
	for i, m := range msgs {
		if m.Role == llm.RoleSystem {
			continue
		}
		content := compactTranscriptContent(m)
		if strings.TrimSpace(content) == "" {
			continue
		}
		contents[i] = content
		size += len(m.Role) + 3 + len(content) + 1
	}
	var b strings.Builder
	b.Grow(size)
	for i, content := range contents {
		if content == "" {
			continue
		}
		b.WriteByte('[')
		b.WriteString(string(msgs[i].Role))
		b.WriteString("] ")
		b.WriteString(content)
		b.WriteByte('\n')
	}
	return b.String()
}

// Preserve Content precedence, separator bytes and existing tool excerpts.
// A single text string can be reused; fragmented text and tool calls require
// only one assembly buffer instead of copying every preceding fragment.
func compactTranscriptContent(m llm.Message) string {
	const toolResultCap = 700
	if IsLegacyCompactionSummary(m) {
		// Resume framing controls the following main request, not this helper.
		// Re-summarize every saved fact verbatim without repeating that framing.
		for _, envelope := range []struct{ preamble, epilogue string }{
			{compactSummaryPreamble, compactSummaryEpilogue},
			{legacyCompactSummaryPreamble, legacyCompactSummaryEpilogue},
		} {
			if hasCompactSummaryEnvelope(m.Content, envelope.preamble, envelope.epilogue) {
				return m.Content[len(envelope.preamble) : len(m.Content)-len(envelope.epilogue)]
			}
		}
	}
	size := len(m.Content)
	first := m.Content
	if m.Content == "" {
		for _, p := range m.Parts {
			if p.Type == llm.PartTypeText {
				if size == 0 {
					first = p.Text
				}
				size += len(p.Text)
			}
		}
	}
	if len(m.ToolCalls) == 0 && size == len(first) {
		if m.Role == llm.RoleTool {
			return compactExcerpt(first, toolResultCap)
		}
		return first
	}
	for _, tc := range m.ToolCalls {
		// UTF-8 boundary adjustment can make the excerpt slightly shorter.
		// Its byte cap is a safe size bound without rendering it twice.
		size += len("\n[tool call: ") + len(tc.Name) + 1 + min(len(tc.Arguments), toolResultCap) + 1
	}
	var b strings.Builder
	b.Grow(size)
	if m.Content != "" {
		b.WriteString(m.Content)
	} else {
		for _, p := range m.Parts {
			if p.Type == llm.PartTypeText {
				b.WriteString(p.Text)
			}
		}
	}
	for _, tc := range m.ToolCalls {
		b.WriteString("\n[tool call: ")
		b.WriteString(tc.Name)
		b.WriteByte(' ')
		b.WriteString(compactExcerpt(tc.Arguments, toolResultCap))
		b.WriteByte(']')
	}
	content := b.String()
	if m.Role == llm.RoleTool {
		return compactExcerpt(content, toolResultCap)
	}
	return content
}

// SummarizeForCompaction asks the provider for the Goal/Done/State/
// Pending summary of msgs. Returns an error when the provider fails
// or produces an empty, incomplete, or non-text answer.
func SummarizeForCompaction(ctx context.Context, provider llm.Provider, msgs []llm.Message) (string, error) {
	transcript := RenderCompactTranscript(msgs)
	if strings.TrimSpace(transcript) == "" {
		return "", fmt.Errorf("nothing to summarize")
	}
	// Label the call for the purpose-metered stats unless the caller
	// already picked a more specific label.
	if llm.PurposeFromContext(ctx) == "" {
		ctx = llm.WithPurpose(ctx, llm.PurposeCompact)
	}
	// Cancel the helper request when a rejected result ends consumption early.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch, err := provider.Complete(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: compactionPrompt},
		{Role: llm.RoleUser, Content: transcript},
	}, nil) // no tools: TEXT ONLY
	if err != nil {
		return "", err
	}
	var b strings.Builder
	var resultErr error
readStream:
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case d, ok := <-ch:
			if !ok {
				break readStream
			}
			if d.Err != nil {
				return "", d.Err
			}
			if resultErr == nil {
				if d.ToolCall != nil {
					resultErr = fmt.Errorf("summarizer returned a tool call instead of text")
				} else if d.FinishReason != "" && d.FinishReason != "stop" {
					resultErr = fmt.Errorf("summarizer did not complete: finish_reason=%q", d.FinishReason)
				}
			}
			// A rejected answer may still have a final usage frame. Drain the
			// stream so the metered provider records it before returning; caller
			// cancellation remains immediate, and unusable text is not retained.
			if resultErr == nil {
				b.WriteString(d.Content)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if resultErr != nil {
		return "", resultErr
	}
	// Reasoning models prepend <thinking> blocks even when told not
	// to; that chain-of-thought would be re-sent with every later
	// request, so strip it before clamping.
	out := strings.TrimSpace(stripThinking(b.String()))
	if out == "" {
		return "", fmt.Errorf("summarizer returned empty text")
	}
	// A stop marker proves stream completion, not that the model summarized
	// anything. Accepting its literal empty template would erase the task state.
	// Keep this check narrow: meaningful summaries can quote an instruction or
	// use different sections, and must not be rejected for formatting alone.
	if sameCompactionInstructionLines(out, compactionTemplate) || sameCompactionInstructionLines(out, compactionPrompt) {
		return "", errCompactionInstructionEcho
	}
	return ClampSummary(out), nil
}

// Ignore blank lines and outer line whitespace, but no wording, punctuation or
// internal whitespace. No semantic inference or additional model call is needed.
func sameCompactionInstructionLines(output, instructions string) bool {
	next := func(s string) (line, rest string) {
		for s != "" {
			line, s, _ = strings.Cut(s, "\n")
			if line = strings.TrimSpace(line); line != "" {
				return line, s
			}
		}
		return "", ""
	}
	for {
		var a, b string
		a, output = next(output)
		b, instructions = next(instructions)
		if a != b {
			return false
		}
		if a == "" {
			return true
		}
	}
}

// clampSummary enforces compactSummaryMaxChars, cutting at the last
// line boundary before the cap when possible.
func ClampSummary(s string) string {
	if len(s) <= compactSummaryMaxChars {
		return s
	}
	cut := strings.LastIndexByte(s[:compactSummaryMaxChars], '\n')
	if cut < compactSummaryMaxChars/2 {
		cut = compactSummaryMaxChars
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + "\n[summary truncated]"
}

// NewAutoSummarizer builds the standard Summarizer wiring: model
// summary + exact facts + resume framing. activeTools is called at
// compaction time so the facts reflect the registry's CURRENT
// tool_search activations (nil = no tool facts).
func NewAutoSummarizer(activeTools func() []string) Summarizer {
	return NewAutoSummarizerWithProvider(nil, activeTools)
}

// NewAutoSummarizerWithProvider optionally pins compaction to a side provider.
// Nil preserves the active-model behaviour. A dedicated local/cloud model
// avoids evicting the coordinator's KV slot and can make summaries cheaper.
// A summarizer may be inherited by a worker. Its owning loop supplies the
// current registry per call instead of using the coordinator's closed-over one.
type compactToolScopeKey struct{}
type compactToolScope struct{ activeTools func() []string }

func NewAutoSummarizerWithProvider(provider llm.Provider, activeTools func() []string) Summarizer {
	return func(ctx context.Context, p llm.Provider, msgs []llm.Message) (string, error) {
		mainProvider := p
		if provider != nil {
			p = provider
		}
		summary, err := SummarizeForCompaction(ctx, p, msgs)
		if err != nil && ctx.Err() == nil && provider != nil && mainProvider != nil && mainProvider != provider {
			// A separately configured cheap summarizer is an optimization, not a
			// new single point of failure. Retry on the active provider only when
			// the side model fails; the default path still makes exactly one call.
			sideErr := err
			summary, err = SummarizeForCompaction(ctx, mainProvider, msgs)
			if err != nil {
				return "", fmt.Errorf("compact_model failed (%w); active-model fallback failed: %w", sideErr, err)
			}
		}
		if err != nil {
			return "", err
		}
		toolNames := activeTools
		if scope, ok := ctx.Value(compactToolScopeKey{}).(compactToolScope); ok {
			toolNames = scope.activeTools // nil is an explicit empty loop scope
		}
		var loaded []string
		if toolNames != nil {
			loaded = toolNames()
		}
		summary += CompactFacts(msgs, loaded)
		return WrapCompactSummary(summary), nil
	}
}

// Keep both the command/header and its terminal status; large patches and file
// bodies must not be replayed in full just to summarize completed work.
func compactExcerpt(s string, budget int) string {
	if len(s) <= budget {
		return s
	}
	const marker = "\n… [middle omitted] …\n"
	room := budget - len(marker)
	head, tail := room/2, len(s)-(room-room/2)
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	for tail < len(s) && !utf8.RuneStart(s[tail]) {
		tail++
	}
	return s[:head] + marker + s[tail:]
}
