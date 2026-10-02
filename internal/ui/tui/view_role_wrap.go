package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Completed user/assistant bodies are already wrapped at width-2. Skip the
// outer pass only for ordinary ASCII/SGR text and an unchanged one-cell gutter.
// Unicode/control input and custom gutter geometry keep the original renderer.
func roleTextFitsWrapHint(label, body string, width int) bool {
	return width > 3 && ordinaryWrappedText(label, width) && ordinaryWrappedText(body, 0)
}

func gutterFitsWrapHint(gutter lipgloss.Style) bool {
	return gutter.GetTransform() == nil && gutter.GetWidth() == 0 && gutter.GetHeight() == 0 && gutter.GetHorizontalFrameSize() == 0 && gutter.GetVerticalFrameSize() == 0
}

func renderRoleBlockWrapHint(label, body string, gutter lipgloss.Style, width int) (string, bool) {
	body = ansi.Wrap(body, width-2, "")
	body = strings.TrimRight(body, "\n")
	fits := true
	if body == "" {
		return label, fits
	}
	lines := strings.Split(body, "\n")
	var b strings.Builder
	b.WriteString(label)
	b.WriteByte('\n')
	var lastPrefix string
	lastPrefixFits := false
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		prefix := gutter.Render("▌")
		if fits {
			if prefix != lastPrefix {
				lastPrefix = prefix
				lastPrefixFits = ordinaryGutter(prefix)
			}
			fits = lastPrefixFits
		}
		b.WriteString(prefix + " ")
		b.WriteString(line)
	}
	return b.String(), fits
}

// Require only ordinary text/complete SGR. Tab/CR/other terminal controls and
// unusual Unicode whitespace are not covered by the wrap-width invariant.
func ordinaryWrappedText(text string, width int) bool {
	column := 0
	for i := 0; i < len(text); {
		ch := text[i]
		if ch == 0x1b {
			end, ok := ordinarySGREnd(text, i)
			if !ok {
				return false
			}
			i = end
			continue
		}
		if ch < 0x80 {
			if (ch < 0x20 && ch != '\n') || ch == 0x7f {
				return false
			}
			if width > 0 {
				if ch == '\n' {
					column = 0
				} else {
					column++
					if column > width {
						return false
					}
				}
			}
			i++
			continue
		}
		// Unicode/grapheme edge cases retain the exact legacy wrapper.
		return false
	}
	return true
}
func ordinarySGREnd(text string, i int) (int, bool) {
	if i+1 >= len(text) || text[i+1] != '[' {
		return 0, false
	}
	for j := i + 2; j < len(text); j++ {
		ch := text[j]
		if ch == 'm' {
			return j + 1, true
		}
		if (ch < '0' || ch > '9') && ch != ';' && ch != ':' {
			return 0, false
		}
	}
	return 0, false
}
func ordinaryGutter(text string) bool {
	seen := false
	for i := 0; i < len(text); {
		if text[i] == 0x1b {
			end, ok := ordinarySGREnd(text, i)
			if !ok {
				return false
			}
			i = end
			continue
		}
		if seen || !strings.HasPrefix(text[i:], "▌") {
			return false
		}
		seen = true
		i += len("▌")
	}
	return seen
}
