package webgui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"supercli/internal/llm"
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

// summarizeHistoryMessageLLM asks the active model for a short topic title
// for the user message. Falls back to summarizeHistoryMessage
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

	// A conversation label describes the topic, not completed work. Keep the
	// off-turn request short and bounded even when the first prompt pasted code.
	topic := summarizeHistoryMessage(text, 512)
	prompt := fmt.Sprintf(`Name the user's topic or request in the same language. One short title, at most %d characters. Return only the title; do not answer the message or describe work done.

Message:
%s`, maxRunes, topic)

	ch, err := prov.Complete(llm.WithBackground(llm.WithPurpose(ctx, llm.PurposeTitle)), []llm.Message{
		{Role: llm.RoleSystem, Content: "You name conversations by their topic."},
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
	for _, tag := range []string{"thinking", "think", "reasoning", "reflection", "thought"} {
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
		"<think>", "</think>",
		"<reasoning>", "</reasoning>",
		"<reflection>", "</reflection>",
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
