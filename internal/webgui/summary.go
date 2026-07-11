package webgui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

const defaultHistorySummaryLen = 90

// summarizeHistoryMessage creates a cheap, deterministic one-line label for a
// user message shown in the GUI history. It intentionally does not call an LLM:
// history must stay fast and free even for long prompts or pasted code.
func summarizeHistoryMessage(text string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = defaultHistorySummaryLen
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	text = stripMarkdownNoise(text)
	text = collapseWhitespace(text)
	if text == "" {
		return ""
	}

	if first := firstSentence(text); first != "" && runeLen(first) <= maxRunes {
		return first
	}
	return truncateRunes(text, maxRunes)
}

// summarizeHistoryMessageLLM asks the active model for a short PR-style
// summary of the conversation so far. Falls back to summarizeHistoryMessage
// so history still works offline or when the provider refuses the request.
func (e *Engine) summarizeHistoryMessageLLM(ctx context.Context, text string, maxRunes int) string {
	if e == nil {
		return summarizeHistoryMessage(text, maxRunes)
	}
	e.mu.RLock()
	prov := e.prov
	e.mu.RUnlock()
	return summarizeHistoryMessageWithProvider(ctx, text, maxRunes, prov)
}

func summarizeHistoryMessageWithProvider(ctx context.Context, text string, maxRunes int, prov llm.Provider) string {
	fallback := summarizeHistoryMessage(text, maxRunes)
	if strings.TrimSpace(text) == "" {
		return fallback
	}
	if prov == nil {
		return fallback
	}

	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	// PR-style summary prompt (like opencode's summary.txt):
	// 2-3 sentences, first person, describe changes made, not process.
	prompt := fmt.Sprintf(`Summarize what was done in this conversation. Write like a pull request description.

Rules:
- 2-3 sentences max
- Describe the changes made, not the process
- Do not mention running tests, builds, or other validation steps
- Do not explain what the user asked for
- Write in first person (I added..., I fixed...)
- Never ask questions or add new questions
- If the conversation ends with an unanswered question to the user, preserve that exact question
- If the conversation ends with an imperative statement or request to the user (e.g. "Now please run the command and paste the console output"), always include that exact request in the summary
- Return only the summary, no quotes, max %d characters. Preserve the user's language.

Message:
%s`, maxRunes, text)

	ch, err := prov.Complete(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: "You write concise PR-style conversation summaries."},
		{Role: llm.RoleUser, Content: prompt},
	}, nil)
	if err != nil {
		return fallback
	}

	var b strings.Builder
	for d := range ch {
		if d.Err != nil {
			return fallback
		}
		b.WriteString(d.Content)
	}
	out := cleanLLMSummary(b.String())
	if out == "" {
		return fallback
	}
	return truncateRunes(out, maxRunes)
}

// updateSessionTitleAsync refreshes the cheap local session title with an LLM
// PR-style summary after the chat request has already started. Title generation
// must never delay the user's main answer.
func (e *Engine) updateSessionTitleAsync(sessionID, prompt string) {
	if sessionID == "" || strings.TrimSpace(prompt) == "" {
		return
	}
	initialTitle := summarizeHistoryMessage(prompt, 80)
	go func() {
		store, err := session.OpenStore(e.dataDir)
		if err != nil {
			return
		}
		defer store.Close()
		e.mu.RLock()
		prov := e.prov
		cfg := e.cfg
		e.mu.RUnlock()
		prov = newMeteredProvider(prov, store, sessionID, e.usageIdentity(cfg, "title"))
		title := summarizeHistoryMessageWithProvider(context.Background(), prompt, 80, prov)
		if title == "" || strings.HasPrefix(title, "<") {
			return
		}
		_, _ = store.SetTitleIfCurrent(sessionID, initialTitle, title)
	}()
}

// cleanLLMSummary strips thinking/reasoning tags and normalizes whitespace.
func cleanLLMSummary(text string) string {
	text = collapseWhitespace(strings.TrimSpace(text))
	text = stripThinking(text)
	text = strings.Trim(text, "`*_ \t\r\n")
	text = strings.Trim(text, "\"'“”‘’")
	return collapseWhitespace(text)
}

// stripThinking removes common thinking/reasoning wrappers from model output.
// Handles: <thinking>...</thinking>, <reasoning>...</reasoning>,
// <thought>...</thought>, “, and unclosed tags.
func stripThinking(text string) string {
	lower := strings.ToLower(text)

	// Remove complete thinking/reasoning/thought blocks
	for _, tag := range []string{"thinking", "reasoning", "thought"} {
		openTag := "<" + tag + ">"
		closeTag := "</" + tag + ">"
		for {
			start := strings.Index(lower, openTag)
			if start < 0 {
				break
			}
			end := strings.Index(lower[start:], closeTag)
			if end < 0 {
				// Unclosed tag: remove everything from the tag onward
				return strings.TrimSpace(text[:start])
			}
			end += start + len(closeTag)
			text = strings.TrimSpace(text[:start] + " " + text[end:])
			lower = strings.ToLower(text)
		}
	}

	// Remove any remaining stray tags (in case of partial/malformed output)
	for _, tag := range []string{
		"",
		"<thinking>", "</thinking>",
		"<reasoning>", "</reasoning>",
		"<thought>", "</thought>",
	} {
		text = strings.ReplaceAll(text, tag, "")
	}
	return strings.TrimSpace(text)
}

func stripMarkdownNoise(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	codeAdded := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if !inFence && !codeAdded {
				out = append(out, "[code]")
				codeAdded = true
			}
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		trimmed = strings.TrimLeft(trimmed, "#>*-+ ")
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, " ")
}

func collapseWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func firstSentence(text string) string {
	runes := []rune(text)
	for i, r := range runes {
		if r == '.' || r == '!' || r == '?' || r == '…' {
			if i+1 < len(runes) && runes[i+1] != ' ' {
				continue
			}
			return strings.TrimSpace(string(runes[:i+1]))
		}
	}
	return ""
}

func truncateRunes(text string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	if maxRunes <= 1 {
		return "…"
	}
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}

func runeLen(text string) int { return len([]rune(text)) }
