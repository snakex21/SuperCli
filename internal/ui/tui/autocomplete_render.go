package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func renderAutocomplete(ac *autocomplete, width int, palette Palette, languages ...string) string {
	language := "en"
	if len(languages) > 0 {
		language = normalizeLanguage(languages[0])
	}
	return renderAutocompleteWithinHeight(ac, width, -1, palette, language)
}

type autocompleteGeometry struct {
	filtered                   []autocompleteItem
	cursor, start, end, height int
	framed, detail, footerRule bool
	footer                     bool
}

// autocompleteLayoutWithin is shared by rendering and height reservation. It
// only filters/counts rows; resizing the popup never changes navigation state.
func autocompleteLayoutWithin(ac *autocomplete, maxHeight int) autocompleteGeometry {
	var g autocompleteGeometry
	if ac == nil || ac.kind == autocompNone || maxHeight == 0 {
		return g
	}
	g.filtered = filterItems(ac.items, ac.query)
	if len(g.filtered) == 0 {
		return g
	}
	g.cursor = minInt(maxInt(0, ac.cursor), len(g.filtered)-1)
	selected := g.filtered[g.cursor]
	g.framed, g.footerRule, g.footer = true, true, true
	g.detail = selected.Desc != "" || selected.Hint != ""
	chrome := 4 // title, footer separator, footer, bottom border
	if g.detail {
		chrome += 2
	}
	if maxHeight > 0 && maxHeight < 3 {
		// One or two rows cannot hold a box: spend them on selectable items.
		g.framed, g.detail, g.footerRule, g.footer = false, false, false, false
		chrome = 0
	} else if maxHeight > 0 {
		if maxHeight < chrome+1 && g.detail {
			g.detail = false
			chrome -= 2
		}
		if maxHeight < chrome+1 {
			g.footerRule = false
			chrome--
		}
		if maxHeight < chrome+1 {
			g.footer = false
			chrome--
		}
	}
	visible := minInt(len(g.filtered), autocompMaxVisible)
	if maxHeight >= 0 {
		visible = minInt(visible, maxHeight-chrome)
	}
	g.start = minInt(maxInt(0, ac.scroll), len(g.filtered)-visible)
	if g.cursor < g.start {
		g.start = g.cursor
	}
	if g.cursor >= g.start+visible {
		g.start = g.cursor - visible + 1
	}
	g.end = minInt(len(g.filtered), g.start+visible)
	g.height = chrome + g.end - g.start
	return g
}

// autocompleteHeightWithin measures the same layout without rendering it.
// Negative budgets preserve the usual eight-item popup; zero hides it.
func autocompleteHeightWithin(ac *autocomplete, maxHeight int) int {
	return autocompleteLayoutWithin(ac, maxHeight).height
}

// renderAutocompleteWithinHeight caps popup rows before drawing any chrome.
// A zero budget hides it; a negative budget preserves the usual desktop layout.
func renderAutocompleteWithinHeight(ac *autocomplete, width, maxHeight int, palette Palette, language string) string {
	g := autocompleteLayoutWithin(ac, maxHeight)
	if g.height == 0 {
		return ""
	}
	language = normalizeLanguage(language)
	if width <= 0 {
		width = 78 // retain the legacy default for an unknown terminal width
	}
	boxWidth := minInt(width-2, 86)
	framed := g.framed && width >= 6
	innerWidth := maxInt(0, boxWidth-2)
	if !framed {
		boxWidth, innerWidth = width, width
	}
	singleLine := strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")
	fit := func(s string, cells int) string {
		if cells <= 0 {
			return ""
		}
		s = ansi.Truncate(singleLine.Replace(s), cells, "…")
		return s + strings.Repeat(" ", maxInt(0, cells-ansi.StringWidth(s)))
	}
	lines := make([]string, 0, g.height)
	row := func(s string) {
		s = fit(s, innerWidth)
		if framed {
			s = palette.Rule.Render("│") + s + palette.Rule.Render("│")
		}
		lines = append(lines, s)
	}
	rule := func(left, right string) string {
		if !framed {
			return palette.Rule.Render(strings.Repeat("─", boxWidth))
		}
		return palette.Rule.Render(left + strings.Repeat("─", innerWidth) + right)
	}
	if g.framed {
		title := textFor(language, "tui.autocomplete_render.b269dc4e81")
		if ac.kind == autocompMention {
			title = textFor(language, "tui.autocomplete_render.abc7e98928")
		}
		if ac.query != "" {
			title += " · " + ac.triggerChar() + ac.query
		}
		if framed {
			title = ansi.Truncate(singleLine.Replace(title), boxWidth-4, "…")
			lines = append(lines, palette.Rule.Render("╭─ ")+palette.PanelTitle.Render(title)+palette.Rule.Render(strings.Repeat("─", boxWidth-4-ansi.StringWidth(title))+"╮"))
		} else {
			lines = append(lines, palette.PanelTitle.Render(fit(title, boxWidth)))
		}
	}
	for i := g.start; i < g.end; i++ {
		it := g.filtered[i]
		cursor := "  "
		labelStyle, valueStyle := palette.Bold, palette.Dim
		if i == g.cursor {
			cursor = "> "
			labelStyle, valueStyle = palette.HeaderMode, palette.StatusValue
		}
		labelWidth := 18
		if ac.kind == autocompMention {
			labelWidth = 28
		}
		if innerWidth < 40 {
			// On phones the command/path matters more than category columns.
			row(labelStyle.Render(cursor + fit(it.Label, maxInt(0, innerWidth-2))))
			continue
		}
		labelWidth = minInt(labelWidth, innerWidth/2)
		const catWidth = 8
		descWidth := maxInt(0, innerWidth-6-labelWidth-catWidth)
		categoryStyle := palette.StatusDim
		if i == g.cursor {
			categoryStyle = palette.StatusValue
		}
		row(labelStyle.Render(cursor+fit(it.Label, labelWidth)) + "  " + categoryStyle.Render(fit(it.Category, catWidth)) + "  " + valueStyle.Render(fit(it.Desc, descWidth)))
	}
	if g.detail {
		selected := g.filtered[g.cursor]
		detail := selected.Desc
		if selected.Hint != "" {
			detail += " · " + selected.Hint
		}
		lines = append(lines, rule("├", "┤"))
		row(palette.InputHint.Render("  " + fit(detail, maxInt(0, innerWidth-2))))
	}
	if g.footerRule {
		lines = append(lines, rule("├", "┤"))
	}
	if g.footer {
		footer := textFor(language, "tui.autocomplete_render.6b1db814c4")
		if ac.kind == autocompMention {
			footer = textFor(language, "tui.autocomplete_render.c25e78bf24")
		}
		row(palette.InputHint.Render(fit(footer, innerWidth)))
	}
	if g.framed {
		lines = append(lines, rule("╰", "╯"))
	}
	return strings.Join(lines, "\n")
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
