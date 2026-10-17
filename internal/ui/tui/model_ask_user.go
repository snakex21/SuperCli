package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// renderAskView produces the full-screen overlay shown when the
// model has called ask_user. The viewport is suspended; the
// user navigates with 1-4 / arrows / space / enter / esc.
func renderAskView(a *pendingAsk, width, height int, languages ...string) string {
	if a.Confirmation {
		return renderConfirmationView(a, width, height)
	}
	language := "en"
	if len(languages) > 0 {
		language = normalizeLanguage(languages[0])
	}
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	if width < 5 || height < 5 {
		return padTo(headerLine(a), width)
	}
	boxW := min(width, 92)
	inner := boxW - 4
	pad := strings.Repeat(" ", (width-boxW)/2)
	question := wrap(a.Question, inner)
	if len(question) > 3 {
		question = append(question[:2], "…")
	}
	var rows []string
	focusStart, focusLabelEnd, focusEnd := 0, 0, 0
	for i, opt := range a.Options {
		marker := "  "
		if i == a.cursor {
			marker = "> "
			focusStart = len(rows)
		}
		check := ""
		if a.MultiSelect {
			check = "[ ] "
			if a.toggled[i] {
				check = "[x] "
			}
		}
		rows = append(rows, wrap(fmt.Sprintf("%s%d. %s%s", marker, i+1, check, opt.Label), inner)...)
		if i == a.cursor {
			focusLabelEnd = len(rows)
		}
		for _, detail := range []string{opt.Description, opt.Preview, opt.Image, opt.ImagePrompt} {
			if detail == "" {
				continue
			}
			lines := wrap(detail, max(1, inner-2))
			if len(lines) > 2 {
				lines = []string{lines[0], padTo(lines[1], max(1, inner-3)) + "…"}
			}
			for _, line := range lines {
				rows = append(rows, "  "+line)
			}
		}
		if i == a.cursor {
			focusEnd = len(rows)
		}
	}
	if a.customMode {
		focusStart = len(rows)
		rows = append(rows, wrap(textFor(language, "tui.model_ask_user.adceb15c66"), inner)...)
		rows = append(rows, wrap(a.custom+"_", inner)...)
		focusLabelEnd, focusEnd = len(rows), len(rows)
	}
	slots := height - 4 // borders, header and keyboard hints always have a row
	labelRows := focusLabelEnd - focusStart
	detailRows := min(1, focusEnd-focusLabelEnd)
	// Keep the question pinned when it fits alongside the selected label and
	// the first description line, rather than spending that space on old choices.
	if len(question)+labelRows+detailRows > slots {
		question = nil
	} else if len(question)+labelRows+detailRows < slots {
		question = append(question, "")
	}
	slots -= len(question)
	if labelRows+detailRows > slots {
		label := compactAskLabel(rows[focusStart:focusLabelEnd], max(1, slots-detailRows), inner)
		visible := append([]string{}, rows[:focusStart]...)
		visible = append(visible, label...)
		rows = append(visible, rows[focusLabelEnd:]...)
		removed := labelRows - len(label)
		focusLabelEnd -= removed
		focusEnd -= removed
	}
	// Scroll the selected block into view, not its first line to the bottom.
	// If details exceed the budget, keep the label and description prefix first.
	start := max(0, min(focusEnd, focusStart+slots)-slots)
	end := min(len(rows), start+slots)
	var lines []string
	lines = append(lines, pad+"┌"+strings.Repeat("─", inner+2)+"┐")
	row := func(text string) { lines = append(lines, pad+"│ "+padTo(text, inner)+" │") }
	row(headerLine(a))
	for _, text := range question {
		row(text)
	}
	for _, text := range rows[start:end] {
		row(text)
	}
	help := helpLine(a, language)
	if ansi.StringWidth(help) > inner {
		switch {
		case a.customMode:
			help = "Enter · Esc"
		case a.MultiSelect:
			help = "↑↓ 1-4 Space Enter Esc"
			if a.AllowCustom {
				help += " c"
			}
		default:
			help = "↑↓ · 1-4 · Enter · Esc"
			if a.AllowCustom {
				help += " · c"
			}
		}
	}
	row(help)
	lines = append(lines, pad+"└"+strings.Repeat("─", inner+2)+"┘")
	return strings.Join(lines, "\n")
}

// compactAskLabel only elides an over-height focused label/input. Preserve its
// beginning (selection marker) and tail (distinctive suffix or input caret).
func compactAskLabel(rows []string, budget, width int) []string {
	if len(rows) <= budget {
		return rows
	}
	if budget <= 2 {
		tail := "…" + rows[len(rows)-1]
		if budget == 1 {
			return []string{"> " + truncateVisibleTail(tail, width-2)}
		}
		return []string{rows[0], truncateVisibleTail(tail, width)}
	}
	head := (budget - 1) / 2
	tail := budget - head - 1
	visible := append([]string{}, rows[:head]...)
	visible = append(visible, "…")
	return append(visible, rows[len(rows)-tail:]...)
}

func headerLine(a *pendingAsk) string {
	if a.Header != "" {
		return "[" + a.Header + "]"
	}
	return "?"
}

func helpLine(a *pendingAsk, languages ...string) string {
	language := "en"
	if len(languages) > 0 {
		language = normalizeLanguage(languages[0])
	}
	if a.customMode {
		return textFor(language, "tui.model_ask_user.989c1f6de0")
	}
	custom := ""
	if a.AllowCustom {
		custom = textFor(language, "tui.model_ask_user.63a72d05fd")
	}
	if a.MultiSelect {
		return textFor(language, "tui.model_ask_user.1b26d0cd22") + custom + textFor(language, "tui.model_ask_user.535083f66f")
	}
	return textFor(language, "tui.model_ask_user.6e61061f0e") + custom + textFor(language, "tui.model_ask_user.535083f66f")
}

// writeCentered writes s centered in width characters, padding
// with spaces. If s is longer than width, it is left as-is.
func writeCentered(b *strings.Builder, s string, width int) {
	if width <= 0 {
		return
	}
	s = ansi.Truncate(s, width, "")
	left := (width - ansi.StringWidth(s)) / 2
	b.WriteString(strings.Repeat(" ", left))
	b.WriteString(padTo(s, width-left))
}

func padTo(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = ansi.Truncate(s, width, "")
	return s + strings.Repeat(" ", max(0, width-ansi.StringWidth(s)))
}

func wrap(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	return strings.Split(ansi.Wrap(s, width, ""), "\n")
}
