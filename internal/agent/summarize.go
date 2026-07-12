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
	"encoding/json"
	"fmt"
	"strings"

	"supercli/internal/llm"
)

// compactionPrompt is the summarization instruction sent to the
// active model for /compact and auto-compaction. Deliberately a
// SHORT forced template: the summary replaces history byte-for-byte
// in every later prompt, and small local models both ramble and get
// lost in long 9-section summaries. Exact facts that need no prose
// (file paths, loaded tools) are appended by code, not the model —
// see CompactFacts.
const compactionPrompt = `Summarize the conversation so far. The summary will REPLACE the older history, so it must let the work continue without the original messages.

Use EXACTLY this template, plain text, no code fences:

Goal: the user's request(s) and intent; quote the key instruction verbatim.
Done: what was accomplished (files created/modified, problems solved, errors fixed).
State: facts needed to continue — key paths, decisions, values, open errors.
Pending: what remains, and the immediate next step.

Hard limit: 500 tokens total. Prefer bare file paths and short phrases over prose. Respond with TEXT ONLY — no tool calls.`

// compactSummaryMaxChars hard-caps the model-produced summary
// (~1000 tokens). Small models overshoot instruction limits; the
// summary is re-sent with EVERY later request, so an unbounded one
// would defeat the point of compacting. Truncation prefers a line
// boundary.
const compactSummaryMaxChars = 4000

// compactSummaryWrapper frames the summary so the model resumes
// seamlessly on the next turn.
const (
	compactSummaryPreamble = "This session is continued from a previous conversation that was compacted to save context. The conversation is summarized below:\n\n"
	compactSummaryEpilogue = "\n\nPlease continue the conversation from where it was left off. Do not ask the user to repeat anything and do not acknowledge this summary in your response."
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
			content += fmt.Sprintf("\n[tool call: %s %s]", tc.Name, tc.Arguments)
		}
		if m.Role == llm.RoleTool && len(content) > toolResultCap {
			content = content[:toolResultCap] + "… [truncated]"
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		fmt.Fprintf(&b, "[%s] %s\n", m.Role, content)
	}
	return b.String()
}

// SummarizeForCompaction asks the provider for the Goal/Done/State/
// Pending summary of msgs. Returns an error when the provider fails
// or produces an empty answer.
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
	ch, err := provider.Complete(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: compactionPrompt},
		{Role: llm.RoleUser, Content: transcript},
	}, nil) // no tools: TEXT ONLY
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for d := range ch {
		if d.Err != nil {
			return "", d.Err
		}
		b.WriteString(d.Content)
	}
	// Reasoning models prepend <thinking> blocks even when told not
	// to; that chain-of-thought would be re-sent with every later
	// request, so strip it before clamping.
	out := strings.TrimSpace(stripThinking(b.String()))
	if out == "" {
		return "", fmt.Errorf("summarizer returned empty text")
	}
	return ClampSummary(out), nil
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
	return strings.TrimSpace(s[:cut]) + "\n[summary truncated]"
}

// fileToolModifies classifies a tool call as writing to the file it
// names (vs merely reading it).
func fileToolModifies(name string) bool {
	for _, p := range []string{"write", "edit", "insert", "delete", "move", "copy", "make", "trash"} {
		if strings.Contains(name, p) {
			return true
		}
	}
	return false
}

// CompactFacts derives cheap EXACT context lines from the compacted
// messages, appended below the model-written summary: bare paths of
// files read and modified (from tool-call arguments) and the tools
// loaded via tool_search (registry state survives compaction, so the
// model must know it doesn't need to search for them again). Costs a
// handful of tokens and spares re-reading after compaction.
func CompactFacts(msgs []llm.Message, loadedTools []string) string {
	const maxList = 20
	seen := map[string]bool{}
	var read, modified []string
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			var args map[string]any
			if json.Unmarshal([]byte(tc.Arguments), &args) != nil {
				continue
			}
			path, _ := args["path"].(string)
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			if fileToolModifies(tc.Name) {
				modified = append(modified, path)
			} else {
				read = append(read, path)
			}
		}
	}
	var b strings.Builder
	writeList := func(label string, list []string) {
		if len(list) == 0 {
			return
		}
		if len(list) > maxList {
			list = list[len(list)-maxList:] // freshest entries win
		}
		fmt.Fprintf(&b, "\n%s: %s", label, strings.Join(list, ", "))
	}
	writeList("files_read", read)
	writeList("files_modified", modified)
	writeList("loaded_tools", loadedTools)
	return b.String()
}

// NewAutoSummarizer builds the standard Summarizer wiring: model
// summary + exact facts + resume framing. activeTools is called at
// compaction time so the facts reflect the registry's CURRENT
// tool_search activations (nil = no tool facts).
func NewAutoSummarizer(activeTools func() []string) Summarizer {
	return func(ctx context.Context, p llm.Provider, msgs []llm.Message) (string, error) {
		summary, err := SummarizeForCompaction(ctx, p, msgs)
		if err != nil {
			return "", err
		}
		var loaded []string
		if activeTools != nil {
			loaded = activeTools()
		}
		summary += CompactFacts(msgs, loaded)
		return WrapCompactSummary(summary), nil
	}
}
