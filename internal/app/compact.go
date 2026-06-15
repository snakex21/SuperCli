// /compact summarization: turn the conversation into a single
// structured summary message instead of merely hiding old turns.
package app

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/llm"
)

// compactionPrompt is the 9-section summarization instruction
// sent to the active model when the user runs /compact.
const compactionPrompt = `Your task is to create a detailed summary of the conversation so far, paying close attention to the user's explicit requests and your previous actions. This summary will REPLACE the conversation history, so it must capture everything needed to continue the work without the original messages.

Structure your summary with these sections:

1. Primary Request and Intent: all of the user's explicit requests and intents, in detail.
2. Key Technical Concepts: technologies, frameworks, and concepts discussed.
3. Files and Code Sections: files examined, modified, or created; include why they matter and key code snippets where load-bearing.
4. Errors and Fixes: every error hit and how it was fixed, including any user feedback on fixes.
5. Problem Solving: problems solved and any ongoing troubleshooting.
6. All User Messages: every non-tool user message, so the user's exact asks are preserved.
7. Pending Tasks: tasks explicitly requested but not yet done.
8. Current Work: precisely what was being worked on immediately before this summary, with file names and code where relevant.
9. Next Step: the next step that directly follows from the most recent work, if any. Quote the most recent instruction verbatim where it defines the next step.

Respond with TEXT ONLY — no tool calls, no code fences around the whole answer.`

// compactSummaryWrapper frames the summary so the model resumes
// seamlessly on the next turn.
const (
	compactSummaryPreamble = "This session is continued from a previous conversation that was compacted to save context. The conversation is summarized below:\n\n"
	compactSummaryEpilogue = "\n\nPlease continue the conversation from where it was left off. Do not ask the user to repeat anything and do not acknowledge this summary in your response."
)

// wrapCompactSummary wraps the model-produced summary in the
// resume framing that replaces the compacted messages.
func wrapCompactSummary(summary string) string {
	return compactSummaryPreamble + strings.TrimSpace(summary) + compactSummaryEpilogue
}

// renderCompactTranscript renders the conversation as plain text
// for the summarizer. System messages are skipped (the summarizer
// gets its own instructions); tool results are truncated so a
// huge file read doesn't blow the summarization call itself.
func renderCompactTranscript(msgs []llm.Message) string {
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

// summarizeForCompaction asks the provider for the 9-section
// summary of msgs. Returns an error when the provider fails or
// produces an empty answer.
func summarizeForCompaction(ctx context.Context, provider llm.Provider, msgs []llm.Message) (string, error) {
	transcript := renderCompactTranscript(msgs)
	if strings.TrimSpace(transcript) == "" {
		return "", fmt.Errorf("nothing to summarize")
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
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", fmt.Errorf("summarizer returned empty text")
	}
	return out, nil
}
