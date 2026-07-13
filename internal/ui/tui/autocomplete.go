package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// autocompleteKind identifies the type of popup autocomplete.
type autocompleteKind int

const (
	autocompNone    autocompleteKind = iota
	autocompSlash                    // /command
	autocompMention                  // @file
)

// autocompleteItem is one entry in the popup list.
type autocompleteItem struct {
	Label    string // display name (command name or file path)
	Desc     string // description (for commands) or file size/type (for files)
	Value    string // value inserted on accept
	Hint     string // argument hint or additional detail
	Category string // command/file category for palette grouping
}

// autocomplete is the state for the popup autocomplete.
type autocomplete struct {
	kind   autocompleteKind
	items  []autocompleteItem // full list
	cursor int                // navigation cursor
	scroll int                // scroll offset (for >8 items)
	query  string             // filter text after trigger
}

const autocompMaxVisible = 8

// triggerChar returns the character that opens this popup.
func (a autocomplete) triggerChar() string {
	switch a.kind {
	case autocompSlash:
		return "/"
	case autocompMention:
		return "@"
	default:
		return ""
	}
}

// --- Slash command autocomplete ---

// buildSlashItems builds autocomplete items from ALL known slash commands.
// We show every command from HelpContentEntries — many are handled inline
// in dispatchSlashCommand (models, providers, goal, plan, export, quit)
// and are NOT in the m.commands map, so we must not filter by it.
func buildSlashItems(_ map[string]SlashHandler) []autocompleteItem {
	entries := HelpContentEntries()
	items := make([]autocompleteItem, 0, len(entries))
	for _, e := range entries {
		category := commandCategory(e.Name)
		items = append(items, autocompleteItem{
			Label:    "/" + e.Name,
			Desc:     e.Desc,
			Hint:     e.Args,
			Category: category,
			Value:    "/" + e.Name + " ",
		})
	}
	return items
}

func commandCategory(name string) string {
	switch name {
	case "help", "status", "cost", "doctor", "sandbox", "allow-all", "context", "mcp", "settings":
		return "system"
	case "model", "providers", "reasoning", "usage":
		return "model"
	case "goal", "plan", "darwin", "council", "reflect", "compact", "workers":
		return "agent"
	case "diff", "undo", "redo", "export", "resume", "clear", "memory":
		return "session"
	case "login", "logout", "account", "accounts":
		return "account"
	default:
		return "command"
	}
}

// HelpContentEntries returns the canonical list of slash commands for autocomplete.
// This is the same data as HelpContent() but structured for programmatic use.
func HelpContentEntries() []SlashEntry {
	return []SlashEntry{
		{Name: "help", Desc: "show this help message"},
		{Name: "goal", Desc: "manage active goal", Args: "<set|list|show|tasks|done> [args]"},
		{Name: "darwin", Desc: "run N parallel agents, pick best", Args: "[N] <prompt>"},
		{Name: "council", Desc: "ask hand-picked models in parallel (no args = pick roster)", Args: "[<prompt>]"},
		{Name: "clear", Desc: "hide recent messages from model context"},
		{Name: "reflect", Desc: "show learned patterns from reflection"},
		{Name: "compact", Desc: "compress context to save tokens"},
		{Name: "status", Desc: "show credits and session info"},
		{Name: "workers", Desc: "list coordinator workers / stop one", Args: "[stop <id>]"},
		{Name: "context", Desc: "show context/token usage breakdown"},
		{Name: "mcp", Desc: "list MCP servers, restart one", Args: "[restart <name>]"},
		{Name: "memory", Desc: "inspect persistent memory", Args: "[search <query> | forget <id>]"},
		{Name: "providers", Desc: "manage providers"},
		{Name: "sandbox", Desc: "show sandbox status"},
		{Name: "allow-all", Desc: "grant full filesystem access", Args: "on|off"},
		{Name: "plan", Desc: "toggle plan mode (read-only analysis)"},
		{Name: "diff", Desc: "show file changes from current session"},
		{Name: "model", Desc: "show or swap active model", Args: "[model_id]"},
		{Name: "reasoning", Desc: "show or set reasoning effort (OpenAI reasoning models)", Args: "[none|minimal|low|medium|high|xhigh|off]"},
		{Name: "resume", Desc: "resume a previous session", Args: "[session_id]"},
		{Name: "export", Desc: "export session to Markdown (arg 'clip' copies to clipboard)", Args: "[filename.md|clip]"},
		{Name: "cost", Desc: "show cost dashboard with per-turn breakdown"},
		{Name: "usage", Desc: "refresh and show ChatGPT-subscription usage limits (Codex)"},
		{Name: "undo", Desc: "revert the last agent turn", Args: ""},
		{Name: "redo", Desc: "restore the last reverted turn", Args: ""},
		{Name: "test", Desc: "run deterministic tests, lint and race checks", Args: "hard"},
		{Name: "settings", Desc: "edit config settings with reset-to-defaults"},
		{Name: "doctor", Desc: "diagnose SuperCli runtime and configuration"},
		{Name: "login", Desc: "sign in with ChatGPT (/login <label> adds another account)", Args: "[label]"},
		{Name: "account", Desc: "show current ChatGPT account and plan"},
		{Name: "accounts", Desc: "list logged-in ChatGPT accounts (round-robin pool)"},
		{Name: "logout", Desc: "remove saved ChatGPT credentials (/logout <label> for one)", Args: "[label]"},
		{Name: "quit", Desc: "exit SuperCli (alias: /exit)"},
	}
}

// --- @file mention autocomplete ---

// buildMentionItems scans the current directory for files and returns
// autocomplete items. Directories get a trailing slash.
func buildMentionItems(home string) []autocompleteItem {
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	items := make([]autocompleteItem, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		// Skip hidden files and .supercli.
		if strings.HasPrefix(name, ".") {
			continue
		}
		label := name
		desc := ""
		if e.IsDir() {
			label = name + "/"
			desc = "dir"
		} else {
			// Show file size.
			info, err := e.Info()
			if err == nil {
				desc = humanSize(info.Size())
			}
		}
		items = append(items, autocompleteItem{
			Label:    label,
			Desc:     desc,
			Value:    "@" + label + " ",
			Category: "file",
		})
	}
	return items
}

// --- Filtering ---

// filterItems returns items whose Label contains the query (case-insensitive).
func filterItems(items []autocompleteItem, query string) []autocompleteItem {
	if query == "" {
		return items
	}
	q := strings.ToLower(query)
	out := make([]autocompleteItem, 0, len(items))
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.Label), q) {
			out = append(out, it)
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, it := range items {
		blob := strings.ToLower(it.Label + " " + it.Desc + " " + it.Hint + " " + it.Category)
		if strings.Contains(strings.ToLower(it.Desc), q) || strings.Contains(strings.ToLower(it.Hint), q) || strings.Contains(strings.ToLower(it.Category), q) || (len(q) >= 3 && fuzzy(blob, q)) {
			out = append(out, it)
		}
	}
	return out
}

// --- Rendering ---

// renderAutocomplete renders the popup as a string (max visible items).
// It's designed to be inserted above the input line in View().
func renderAutocomplete(ac *autocomplete, width int, palette Palette) string {
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
	title := "Commands"
	if ac.kind == autocompMention {
		title = "Files"
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
			cursor = "❯ "
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
	footer := "  ↑/↓ select · Tab insert · Enter run · Esc close"
	if ac.kind == autocompMention {
		footer = "  ↑/↓ select · Tab insert · Enter insert · Esc close"
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
