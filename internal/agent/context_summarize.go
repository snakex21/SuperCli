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

// Compaction writes a working-state note (after 1.0.40): the older history is
// replaced by a structured note of what the work needs to continue, updated
// on every later compaction instead of re-summarized from scratch, and the
// full transcript stays searchable with session_recall. The sections are
// fixed so a later update finds every part of the earlier note.
//
// Keep the placeholders shared with the narrow empty-template echo check.
const compactionTemplate = `Goal: the overall goal and the user's current request (quote the key instruction).
User decisions and constraints: every still-valid preference, prohibition and style rule, verbatim when short.
Done: completed and verified work: files changed, commits, checks that passed.
In progress and next: unfinished work and the immediate next step.
Bugs and open questions: known failures, errors and unanswered questions.
Workers: id, task, status and last result of each worker that still matters.
Key facts and paths: paths, commands, values and names needed to continue.`

const compactionNoteRules = `If the conversation starts with an earlier state note (or a summary), UPDATE it: keep every line that is still true, change only what newer messages change, add new items. Never drop a user decision or constraint unless the user withdrew it.
Tool/task results are authoritative; intentions/promises are not Done. Process launch/exit is not server readiness; keep last observed status, readiness evidence and time distinct; old checks are not current health.
A successful worker's returned investigation is not pending work. Keep failed worker IDs; continue them with send_message when useful instead of restarting.
The full transcript stays searchable with session_recall: prefer exact pointers (paths, names, what to search for) over long excerpts.
As long as the state needs and no longer: usually 300-3,000 tokens, up to 20,000 for long complex work, never more. TEXT ONLY - no tool calls.`

// compactionPrompt is the standalone instruction (no verbatim carry).
const compactionPrompt = `Write the working-state note that REPLACES the older history so the work continues without it.

Use exactly these sections, plain text, no code fences:
` + compactionTemplate + `

` + compactionNoteRules

// The auto-summarizer appends an exact human/legacy instruction carry and
// bounded tool facts; the note keeps the decisions they imply without
// copying those catalogs. Same sections as the standalone template.
const compactionStateTemplate = compactionTemplate

const compactionStatePrompt = `Write the working-state note that REPLACES the older history so the work continues without it.

Use exactly these sections, plain text, no code fences:
` + compactionStateTemplate + `

The caller separately keeps the original user requests verbatim and a file/tool fact catalog: do not copy those catalogs, but keep the decisions and constraints they set in the note.
` + compactionNoteRules

var errCompactionInstructionEcho = errors.New("compact: model repeated summarization instructions instead of conversation facts; history preserved")
var errCompactionSummaryTooLong = errors.New("compact: model summary exceeds byte limit; history preserved")

// compactSummaryMaxChars is the hard byte cap of a state note (~20k tokens).
// A note is re-sent with every later request; an oversized one is rejected
// rather than cut, so its tail (pending work, constraints) is never lost.
// compactNoteMaxChars lowers it for small windows.
const compactSummaryMaxChars = 60000

// compactLegacySummaryChars sizes the "small history" heuristics of manual
// compaction (compactSplit), which predate the larger state note.
const compactLegacySummaryChars = 4000

// compactNoteMaxChars is the note cap for a window: at most ~1/8 of it.
func compactNoteMaxChars(window int) int {
	if window <= 0 {
		return compactSummaryMaxChars
	}
	return max(min(compactSummaryMaxChars, window*3/8), compactLegacySummaryChars)
}

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
	return renderCompactTranscript(msgs, 0)
}

// compactOlderTextCap bounds one older user/assistant text in the summary
// input; head and tail survive around an explicit omission marker.
const compactOlderTextCap = 3000

// compactSummaryInput is the helper transcript: messages before the two
// newest user turns have long texts capped. Tool excerpts, prior summaries
// and the recent turns stay byte-identical to RenderCompactTranscript.
func compactSummaryInput(msgs []llm.Message) string {
	return renderCompactTranscript(msgs, autoCompactSplit(msgs))
}

// compactCaps bound one message in the summary input: older user/assistant
// texts (head and tail kept) and tool results and call arguments (excerpt).
type compactCaps struct{ older, tool int }

// defaultCompactCaps is the normal summary input; tighter caps are used only
// when that input would not fit the summarizing model's window.
var defaultCompactCaps = compactCaps{older: compactOlderTextCap, tool: compactToolResultCap}

// capBefore: user/assistant texts at indices below it are capped.
func renderCompactTranscript(msgs []llm.Message, capBefore int) string {
	return renderCompactTranscriptCaps(msgs, capBefore, defaultCompactCaps)
}

func renderCompactTranscriptCaps(msgs []llm.Message, capBefore int, caps compactCaps) string {
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
		if i < capBefore {
			m = capCompactText(m, caps.older)
		}
		content := compactTranscriptContentCap(m, caps.tool)
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

// capCompactText returns a copy of an older user/assistant message whose text
// keeps head and tail. Tool results/calls and prior summaries are unchanged.
func capCompactText(m llm.Message, budget int) llm.Message {
	if (m.Role != llm.RoleUser && m.Role != llm.RoleAssistant) || IsLegacyCompactionSummary(m) {
		return m
	}
	text := m.Content
	if text == "" {
		size := 0
		for _, p := range m.Parts {
			if p.Type == llm.PartTypeText {
				size += len(p.Text)
			}
		}
		if size <= budget {
			return m
		}
		var b strings.Builder
		b.Grow(size)
		for _, p := range m.Parts {
			if p.Type == llm.PartTypeText {
				b.WriteString(p.Text)
			}
		}
		text = b.String()
	}
	if len(text) <= budget {
		return m
	}
	m.Content, m.Parts = compactHeadTail(text, budget), nil
	return m
}

// compactHeadTail keeps ~2/3 head (instructions) and ~1/3 tail (outcome).
func compactHeadTail(s string, budget int) string {
	head := budget * 2 / 3
	tail := len(s) - (budget - head)
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	for tail < len(s) && !utf8.RuneStart(s[tail]) {
		tail++
	}
	return s[:head] + fmt.Sprintf("\n[... %d chars omitted ...]\n", utf8.RuneCountInString(s[head:tail])) + s[tail:]
}

// Preserve Content precedence, separator bytes and existing tool excerpts.
// A single text string can be reused; fragmented text and tool calls require
// only one assembly buffer instead of copying every preceding fragment.
func compactTranscriptContent(m llm.Message) string {
	return compactTranscriptContentCap(m, compactToolResultCap)
}

// compactToolResultCap bounds one tool result or call argument in the
// normal summary input.
const compactToolResultCap = 700

func compactTranscriptContentCap(m llm.Message, toolResultCap int) string {
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
// or produces an empty, incomplete, oversized, or non-text answer.
func SummarizeForCompaction(ctx context.Context, provider llm.Provider, msgs []llm.Message) (string, error) {
	return summarizeForCompaction(ctx, provider, msgs, compactionPrompt)
}

// summarizeTranscript makes one summary request for an already rendered
// transcript (see summarizeForCompaction for the window-bounded input).
func summarizeTranscript(ctx context.Context, provider llm.Provider, transcript, instruction string) (string, error) {
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
		{Role: llm.RoleSystem, Content: instruction},
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
	// request, so strip it before validating the visible summary's size.
	out := strings.TrimSpace(stripThinking(b.String()))
	if out == "" {
		return "", fmt.Errorf("summarizer returned empty text")
	}
	// A stop marker proves stream completion, not that the model summarized
	// anything. Accepting its literal empty template would erase the task state.
	// Keep this check narrow: meaningful summaries can quote an instruction or
	// use different sections, and must not be rejected for formatting alone.
	if sameCompactionInstructionLines(out, compactionTemplate) || sameCompactionInstructionLines(out, compactionPrompt) ||
		sameCompactionInstructionLines(out, compactionStateTemplate) || sameCompactionInstructionLines(out, compactionStatePrompt) {
		return "", errCompactionInstructionEcho
	}
	// A completed stream can still exceed the requested limit. Its tail may
	// contain the only State/Pending instructions, so never accept a cut-off
	// replacement for the original history. Usage has already been drained.
	if limit := compactNoteMaxChars(compactWindowFrom(ctx)); len(out) > limit {
		return "", fmt.Errorf("%w: %d bytes, maximum %d", errCompactionSummaryTooLong, len(out), limit)
	}
	return out, nil
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
	var resolve func(context.Context) (llm.Provider, error)
	if provider != nil {
		resolve = func(context.Context) (llm.Provider, error) { return provider, nil }
	}
	return NewAutoSummarizerWithProviderResolver(resolve, activeTools)
}

// NewAutoSummarizerWithProviderResolver defers an optional backend's construction
// until summarization is needed. A construction error falls back to the active
// provider, like the existing optional-provider wiring; cancellation preserves
// history and does not launch a fallback. The resolver returns a real provider.
func NewAutoSummarizerWithProviderResolver(resolve func(context.Context) (llm.Provider, error), activeTools func() []string) Summarizer {
	return func(ctx context.Context, p llm.Provider, msgs []llm.Message) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		mainProvider := p
		var provider llm.Provider
		if resolve != nil {
			var resolveErr error
			provider, resolveErr = resolve(ctx)
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if resolveErr != nil {
				provider = nil
			}
		}
		if provider != nil {
			p = provider
		}
		summary, err := summarizeForCompaction(ctx, p, msgs, compactionStatePrompt)
		if err != nil && ctx.Err() == nil && provider != nil && mainProvider != nil && mainProvider != provider {
			// A separately configured cheap summarizer is an optimization, not a
			// new single point of failure. Retry on the active provider only when
			// the side model fails; the default path still makes exactly one call.
			sideErr := err
			summary, err = summarizeForCompaction(ctx, mainProvider, msgs, compactionStatePrompt)
			if err != nil {
				return "", fmt.Errorf("compact_model failed (%w); active-model fallback failed: %w", sideErr, err)
			}
		}
		if err != nil {
			return "", err
		}
		if sink, ok := ctx.Value(compactNoteSinkKey{}).(*string); ok {
			*sink = summary // the note as the model wrote it, for the UI
		}
		toolNames := activeTools
		if scope, ok := ctx.Value(compactToolScopeKey{}).(compactToolScope); ok {
			toolNames = scope.activeTools // nil is an explicit empty loop scope
		}
		var loaded []string
		if toolNames != nil {
			loaded = toolNames()
		}
		summary += compactPreservedUserContextWithin(msgs, compactWindowFrom(ctx))
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
