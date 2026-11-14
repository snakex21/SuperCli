package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// markdownHeading parses "# x" .. "###### x". The marker needs a space, so
// "#hashtag" and "#123" stay ordinary text.
func markdownHeading(line string) (level int, body string, ok bool) {
	for level < len(line) && level < 6 && line[level] == '#' {
		level++
	}
	if level == 0 || level >= len(line) || line[level] != ' ' {
		if level > 0 && level == len(line) {
			return level, "", true // "##" alone: an empty heading
		}
		return 0, "", false
	}
	return level, strings.TrimSpace(line[level+1:]), true
}

// renderMarkdownBlock renders the block starting at lines[i]: a fenced code
// block, a table (header row followed by a |---| separator) or one line. It
// returns the rendered text and the index of the next unrendered line. The
// work is proportional to the block, so the streaming active-section cache
// keeps its cost profile.
func renderMarkdownBlock(lines []string, i int, p Palette) (string, int) {
	line := lines[i]
	if isFenceLine(line) {
		return renderFencedBlock(lines, i, p)
	}
	if isTableRow(line) && i+1 < len(lines) && isTableSeparator(lines[i+1]) {
		end := i + 2
		for end < len(lines) && isTableRow(lines[end]) {
			end++
		}
		return renderMarkdownTable(lines[i], lines[i+1], lines[i+2:end], p), end
	}
	return renderMarkdownLine(line, p), i + 1
}

func isTableRow(line string) bool {
	t := strings.TrimSpace(line)
	return len(t) > 1 && t[0] == '|' && strings.Count(t, "|") >= 2
}

func isTableSeparator(line string) bool {
	if !isTableRow(line) {
		return false
	}
	for _, cell := range tableCells(line) {
		c := strings.TrimSpace(cell)
		if c == "" || strings.Trim(c, ":-") != "" || !strings.Contains(c, "-") {
			return false
		}
	}
	return true
}

func tableCells(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	cells := strings.Split(t, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// renderMarkdownTable aligns columns by display width (CJK and emoji
// included) and honours :--, :-: and --: alignment.
func renderMarkdownTable(header, separator string, body []string, p Palette) string {
	rows := [][]string{tableCells(header)}
	for _, line := range body {
		rows = append(rows, tableCells(line))
	}
	aligns := tableCells(separator)
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	rendered := make([][]string, len(rows))
	widths := make([]int, cols)
	for r, cells := range rows {
		rendered[r] = make([]string, cols)
		for c := 0; c < cols; c++ {
			text := ""
			if c < len(cells) {
				text = applyInlineMarkdown(cells[c], p)
			}
			if r == 0 {
				text = p.Bold.Render(text)
			}
			rendered[r][c] = text
			widths[c] = max(widths[c], ansi.StringWidth(text))
		}
	}
	bar := p.MdFrame.Render(" │ ")
	var b strings.Builder
	for r, cells := range rendered {
		if r > 0 {
			b.WriteByte('\n')
		}
		var line strings.Builder
		for c, text := range cells {
			if c > 0 {
				line.WriteString(bar)
			}
			pad := widths[c] - ansi.StringWidth(text)
			align := ""
			if c < len(aligns) {
				align = aligns[c]
			}
			switch {
			case strings.HasPrefix(align, ":") && strings.HasSuffix(align, ":"):
				line.WriteString(strings.Repeat(" ", pad/2) + text + strings.Repeat(" ", pad-pad/2))
			case strings.HasSuffix(align, ":"):
				line.WriteString(strings.Repeat(" ", pad) + text)
			default:
				line.WriteString(text + strings.Repeat(" ", pad))
			}
		}
		// No trailing padding after the last column.
		b.WriteString(strings.TrimRight(line.String(), " "))
		if r == 0 {
			parts := make([]string, cols)
			for c := range parts {
				parts[c] = strings.Repeat("─", max(1, widths[c]))
			}
			b.WriteString("\n" + p.MdFrame.Render(strings.Join(parts, "─┼─")))
		}
	}
	return b.String()
}
