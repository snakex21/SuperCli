package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func renderAutocomplete(ac *autocomplete, width int, palette Palette, languages ...string) string {
	language := "en"
	if len(languages) > 0 {
		language = normalizeLanguage(languages[0])
	}
	if ac == nil || ac.kind == autocompNone {
		return ""
	}
	filtered := filterItems(ac.items, ac.query)
	if len(filtered) == 0 {
		return ""
	}

	// Clamp cursor and scroll.
	total := len(filtered)
	if ac.cursor >= total {
		ac.cursor = total - 1
	}
	if ac.cursor < 0 {
		ac.cursor = 0
	}
	if ac.cursor < ac.scroll {
		ac.scroll = ac.cursor
	}
	if ac.cursor >= ac.scroll+autocompMaxVisible {
		ac.scroll = ac.cursor - autocompMaxVisible + 1
	}

	visible := filtered[ac.scroll:]
	if len(visible) > autocompMaxVisible {
		visible = visible[:autocompMaxVisible]
	}

	boxWidth := width - 2
	if boxWidth <= 0 {
		boxWidth = 76
	}
	if boxWidth > 86 {
		boxWidth = 86
	}
	if boxWidth < 42 {
		boxWidth = 42
	}
	innerWidth := boxWidth - 2
	labelWidth := 18
	if ac.kind == autocompMention {
		labelWidth = 28
	}
	if labelWidth > innerWidth/2 {
		labelWidth = innerWidth / 2
	}
	catWidth := 8
	descWidth := innerWidth - 4 - labelWidth - catWidth
	if descWidth < 10 {
		descWidth = 10
	}

	var b strings.Builder
	title := textFor(language, "Commands", "Polecenia")
	if ac.kind == autocompMention {
		title = textFor(language, "Files", "Pliki")
	}
	if ac.query != "" {
		title += " · " + ac.triggerChar() + ac.query
	}
	b.WriteString(palette.Rule.Render("╭─ "))
	b.WriteString(palette.PanelTitle.Render(title))
	titlePad := boxWidth - lipgloss.Width("╭─ ") - lipgloss.Width(title) - 1
	if titlePad < 0 {
		titlePad = 0
	}
	b.WriteString(palette.Rule.Render(strings.Repeat("─", titlePad) + "╮"))
	b.WriteString("\n")

	for i, it := range visible {
		globalIdx := ac.scroll + i
		cursor := "  "
		if globalIdx == ac.cursor {
			cursor = "> "
		}

		label := padRight(truncateText(it.Label, labelWidth), labelWidth)
		cat := padRight(truncateText(it.Category, catWidth), catWidth)
		desc := truncateText(it.Desc, descWidth)
		line := cursor + palette.Bold.Render(label) + "  " + palette.StatusDim.Render(cat) + "  " + palette.Dim.Render(desc)

		if globalIdx == ac.cursor {
			line = palette.HeaderMode.Render(cursor) + palette.HeaderMode.Render(label) + "  " + palette.StatusValue.Render(cat) + "  " + palette.StatusValue.Render(desc)
		}
		b.WriteString(palette.Rule.Render("│"))
		b.WriteString(line)
		remaining := innerWidth - lipgloss.Width(stripStyleText(cursor+label+"  "+cat+"  "+desc))
		if remaining > 0 {
			b.WriteString(strings.Repeat(" ", remaining))
		}
		b.WriteString(palette.Rule.Render("│"))
		b.WriteString("\n")
	}
	selected := filtered[minInt(ac.cursor, len(filtered)-1)]
	detail := selected.Desc
	if selected.Hint != "" {
		detail += " · " + selected.Hint
	}
	if detail != "" {
		b.WriteString(palette.Rule.Render("├" + strings.Repeat("─", boxWidth-2) + "┤"))
		b.WriteString("\n")
		detailLine := "  " + truncateText(detail, innerWidth-2)
		b.WriteString(palette.Rule.Render("│"))
		b.WriteString(palette.InputHint.Render(detailLine))
		if pad := innerWidth - lipgloss.Width(detailLine); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
		b.WriteString(palette.Rule.Render("│"))
		b.WriteString("\n")
	}
	footer := textFor(language, "  ↑/↓ select · Tab insert · Enter run · Esc close", "  ↑/↓ wybierz · Tab wstaw · Enter uruchom · Esc zamknij")
	if ac.kind == autocompMention {
		footer = textFor(language, "  ↑/↓ select · Tab insert · Enter insert · Esc close", "  ↑/↓ wybierz · Tab wstaw · Enter wstaw · Esc zamknij")
	}
	b.WriteString(palette.Rule.Render("├" + strings.Repeat("─", boxWidth-2) + "┤"))
	b.WriteString("\n")
	b.WriteString(palette.Rule.Render("│"))
	b.WriteString(palette.InputHint.Render(truncateText(footer, innerWidth)))
	if pad := innerWidth - lipgloss.Width(truncateText(footer, innerWidth)); pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	}
	b.WriteString(palette.Rule.Render("│"))
	b.WriteString("\n")

	b.WriteString(palette.Rule.Render("╰" + strings.Repeat("─", boxWidth-2) + "╯"))

	return b.String()
}

func truncateText(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)+"…") > width {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func padRight(s string, width int) string {
	pad := width - lipgloss.Width(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

func stripStyleText(s string) string { return s }

// --- Helpers ---

// humanSize returns a human-readable file size.
func humanSize(bytes int64) string {
	switch {
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// resolveAutocompleteHome resolves the home/working directory for @mentions.
func resolveAutocompleteHome(home string) string {
	if home != "" {
		return home
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

// splitAutocompleteTrigger checks if the input text ends with a trigger
// character (/ or @) and returns the kind and current query after the trigger.
// Returns autocompNone if no trigger is active.
func splitAutocompleteTrigger(text string) (autocompleteKind, string) {
	// Find the trigger character. We look for / or @ that is either
	// at position 0 (for /) or preceded by a space/newline (for both).
	if text == "" {
		return autocompNone, ""
	}

	// Check for @mention — trigger after space or at start.
	for i := len(text) - 1; i >= 0; i-- {
		ch := text[i]
		if ch == '@' {
			if i == 0 || text[i-1] == ' ' || text[i-1] == '\t' || text[i-1] == '\n' {
				return autocompMention, text[i+1:]
			}
			// @ found but not a trigger — stop scanning.
			return autocompNone, ""
		}
		if ch == '/' && i == 0 {
			return autocompSlash, text[i+1:]
		}
		if ch == ' ' || ch == '\t' || ch == '\n' {
			// Hit a space without finding trigger — no active trigger.
			return autocompNone, ""
		}
	}
	// We're at the very beginning or only have non-space chars.
	if text[0] == '/' {
		return autocompSlash, text[1:]
	}
	if text[0] == '@' {
		return autocompMention, text[1:]
	}
	return autocompNone, ""
}

// parentDir returns the parent directory portion of a file path
// for @file autocomplete, so "./src/foo" resolves to "src/".
func parentDir(p string) string {
	dir := filepath.Dir(p)
	if dir == "." {
		return ""
	}
	return dir
}
