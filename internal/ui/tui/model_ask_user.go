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
	rows := wrap(a.Question, inner)
	if len(rows) > 3 {
		rows = append(rows[:2], "…")
	}
	rows = append(rows, "")
	focus := 0
	for i, opt := range a.Options {
		marker := "  "
		if i == a.cursor {
			marker = "> "
			focus = len(rows)
		}
		check := ""
		if a.MultiSelect {
			check = "[ ] "
			if a.toggled[i] {
				check = "[x] "
			}
		}
		rows = append(rows, wrap(fmt.Sprintf("%s%d. %s%s", marker, i+1, check, opt.Label), inner)...)
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
	}
	if a.customMode {
		focus = len(rows)
		rows = append(rows, textFor(language, "Your answer:", "Twoja odpowiedź:"))
		rows = append(rows, wrap(a.custom+"_", inner)...)
		focus = len(rows) - 1
	}
	slots := height - 4
	start := 0
	if focus >= slots {
		start = focus - slots + 1
	}
	end := min(len(rows), start+slots)
	var lines []string
	lines = append(lines, pad+"┌"+strings.Repeat("─", inner+2)+"┐")
	row := func(text string) { lines = append(lines, pad+"│ "+padTo(text, inner)+" │") }
	row(headerLine(a))
	for _, text := range rows[start:end] {
		row(text)
	}
	help := helpLine(a, language)
	if ansi.StringWidth(help) > inner {
		help = "↑↓ · 1-4 · Enter · Esc"
		if a.AllowCustom {
			help += " · c"
		}
	}
	row(help)
	lines = append(lines, pad+"└"+strings.Repeat("─", inner+2)+"┘")
	return strings.Join(lines, "\n")
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
		return textFor(language, "type answer · ⏎ submit · Esc options", "wpisz odpowiedź · ⏎ wyślij · Esc opcje")
	}
	custom := ""
	if a.AllowCustom {
		custom = textFor(language, " · c custom", " · c własna")
	}
	if a.MultiSelect {
		return textFor(language, "1-4 toggle · ↑↓ move · ⏎ confirm", "1-4 przełącz · ↑↓ wybierz · ⏎ potwierdź") + custom + textFor(language, " · Esc cancel", " · Esc anuluj")
	}
	return textFor(language, "1-4 quick pick · ↑↓ move · ⏎ confirm", "1-4 szybki wybór · ↑↓ wybierz · ⏎ potwierdź") + custom + textFor(language, " · Esc cancel", " · Esc anuluj")
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
