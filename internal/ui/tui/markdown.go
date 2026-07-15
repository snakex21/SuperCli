package tui

import (
	"regexp"
	"strings"
)

var (
	reBold        = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reCode        = regexp.MustCompile("`([^`]+)`")
	reXMLToolCall = regexp.MustCompile(`(?s)<tool_call>.*?</tool_call>`)

	// thinkingPrefixes: if a line starts with one of these
	// (case-insensitive, trimmed), it's treated as model
	// "thinking" even without <thinking> tags. Common in
	// models that don't use the native thinking format.
	thinkingPrefixes = []string{
		"the user", "i should", "i need to", "actually",
		"let me", "i'll", "i might", "i can", "i think",
		"i will", "i am", "i'm", "i want", "i would",
		"first", "next", "now i", "so i", "this is",
	}
)

// renderAssistantMarkdown applies basic markdown formatting,
// <thinking> block styling, and heuristic thinking detection
// to assistant text. collapsed controls whether completed
// thinking blocks are hidden.
func renderAssistantMarkdown(text string, p Palette, collapsed bool, languages ...string) string {
	language := "en"
	if len(languages) > 0 {
		language = normalizeLanguage(languages[0])
	}
	// Strip XML tool call blocks — they're an internal
	// fallback format and should never appear in the TUI.
	text = reXMLToolCall.ReplaceAllString(text, "")

	segments := splitThinking(text)
	if len(segments) <= 1 && !segments[0].thinking {
		// No <thinking> tags. Run heuristic detection.
		return renderWithHeuristicThinking(text, p, collapsed, language)
	}
	var b strings.Builder
	for _, seg := range segments {
		if seg.thinking {
			b.WriteString(renderThinkingBlock(seg.text, p, collapsed, language))
		} else {
			// Apply heuristic to non-thinking segments too.
			b.WriteString(renderWithHeuristicThinking(seg.text, p, collapsed, language))
		}
	}
	return b.String()
}

// renderWithHeuristicThinking scans lines for thinking-like
// patterns (lines starting with "The user", "I should", etc.)
// and renders them as dimmed thinking blocks.
func renderWithHeuristicThinking(text string, p Palette, collapsed bool, languages ...string) string {
	language := "en"
	if len(languages) > 0 {
		language = normalizeLanguage(languages[0])
	}
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	inThinking := false
	var thinkBuf []string

	flushThinking := func() {
		if len(thinkBuf) > 0 {
			block := strings.Join(thinkBuf, "\n")
			b.WriteString(renderThinkingBlock(block, p, collapsed, language))
			thinkBuf = nil
		}
		inThinking = false
	}

	for i, line := range lines {
		if isHeuristicThinkingLine(line) && !isDefinitelyResponseLine(line) {
			if !inThinking {
				// Flush any pending normal text first.
				if i > 0 && len(thinkBuf) == 0 {
					// Previous lines were normal — already written.
				}
				inThinking = true
			}
			thinkBuf = append(thinkBuf, line)
		} else {
			if inThinking {
				flushThinking()
			}
			if i > 0 || len(thinkBuf) == 0 {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
			}
			b.WriteString(renderMarkdownLine(line, p))
		}
	}
	flushThinking()
	return b.String()
}

// isHeuristicThinkingLine returns true if the line looks like
// model reasoning rather than user-facing response.
func isHeuristicThinkingLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}
	for _, prefix := range thinkingPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// isDefinitelyResponseLine returns true for lines that are
// clearly user-facing content (headings, lists, code blocks).
func isDefinitelyResponseLine(line string) bool {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "##") || strings.HasPrefix(t, "```") {
		return true
	}
	if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
		return true
	}
	// Numbered list: "1. text"
	if len(t) >= 3 && t[0] >= '0' && t[0] <= '9' && t[1] == '.' && t[2] == ' ' {
		return true
	}
	return false
}

type textSegment struct {
	text     string
	thinking bool
}

// splitThinking splits text by <thinking>...</thinking> tags.
// Unclosed <thinking> (streaming) renders as thinking with
// "..." suffix to indicate it's still generating.
func splitThinking(text string) []textSegment {
	if !strings.Contains(text, "<thinking>") {
		return []textSegment{{text: text, thinking: false}}
	}
	var out []textSegment
	parts := strings.Split(text, "<thinking>")
	// First part is before any <thinking>.
	if parts[0] != "" {
		out = append(out, textSegment{text: parts[0], thinking: false})
	}
	for _, p := range parts[1:] {
		if idx := strings.Index(p, "</thinking>"); idx >= 0 {
			// Closed thinking block.
			think := p[:idx]
			rest := p[idx+len("</thinking>"):]
			if think != "" {
				out = append(out, textSegment{text: think, thinking: true})
			}
			if rest != "" {
				out = append(out, textSegment{text: rest, thinking: false})
			}
		} else {
			// Unclosed — streaming in progress.
			out = append(out, textSegment{text: p, thinking: true})
		}
	}
	return out
}

// renderThinkingBlock renders a thinking segment.
// When collapsed and the block is closed, shows a summary line.
// Streaming (unclosed) blocks are never collapsed.
func renderThinkingBlock(text string, p Palette, collapsed bool, languages ...string) string {
	language := "en"
	if len(languages) > 0 {
		language = normalizeLanguage(languages[0])
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if collapsed {
		return p.MdThinkingHeader.Render(textFor(language, "Thinking (hidden — T to expand)", "Myślenie (ukryte — T rozwija)")) + "\n"
	}
	var b strings.Builder
	b.WriteString(p.MdThinkingHeader.Render(textFor(language, "Thinking:", "Myślenie:")))
	b.WriteByte('\n')
	for _, line := range strings.Split(text, "\n") {
		b.WriteString(p.MdThinking.Render("  " + line))
		b.WriteByte('\n')
	}
	return b.String()
}

// renderMarkdownBody renders a non-thinking text segment with
// standard markdown formatting (headings, lists, bold, code).
func renderMarkdownBody(text string, p Palette) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(renderMarkdownLine(line, p))
	}
	return b.String()
}

// renderMarkdownLine formats a single line of assistant text.
func renderMarkdownLine(line string, p Palette) string {
	trimmed := strings.TrimRight(line, " \t")

	// ## heading → cyan bold (strip prefix, apply inline formatting)
	if strings.HasPrefix(trimmed, "## ") {
		body := trimmed[3:]
		body = applyInlineMarkdown(body, p)
		return p.MdH2.Render("## " + body)
	}
	// ### subheading → yellow bold
	if strings.HasPrefix(trimmed, "### ") {
		body := trimmed[4:]
		body = applyInlineMarkdown(body, p)
		return p.MdH3.Render("### " + body)
	}

	// - list item → bullet
	if strings.HasPrefix(trimmed, "- ") {
		trimmed = "\u2022 " + trimmed[2:]
	}
	// * list item → bullet (but not **bold** which starts with **)
	if strings.HasPrefix(trimmed, "* ") && !strings.HasPrefix(trimmed, "**") {
		trimmed = "\u2022 " + trimmed[2:]
	}

	// Apply inline formatting, then wrap in assistant color.
	styled := applyInlineMarkdown(trimmed, p)
	return p.Assistant.Render(styled)
}

// applyInlineMarkdown applies **bold** and `code` spans within
// a single line. Uses regex replace with lipgloss styles.
func applyInlineMarkdown(line string, p Palette) string {
	// `code` — must be applied BEFORE **bold** so that backticks
	// inside bold text don't interfere.
	line = reCode.ReplaceAllStringFunc(line, func(match string) string {
		inner := match[1 : len(match)-1]
		return p.MdCode.Render(inner)
	})

	// **bold**
	line = reBold.ReplaceAllStringFunc(line, func(match string) string {
		inner := match[2 : len(match)-2]
		return p.Bold.Render(inner)
	})

	return line
}
