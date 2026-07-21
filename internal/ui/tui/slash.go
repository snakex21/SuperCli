package tui

import (
	"context"
	"fmt"
	"strings"
)

// SlashCommand represents a TUI-level command the user
// invokes with a leading "/" (e.g. "/darwin 3 fix bug").
type SlashCommand struct {
	Name string
	Args string
	// Quiet suppresses the technical command bubble when a visual menu
	// invokes an existing handler on the user's behalf.
	Quiet bool
}

// ParseSlashCommand returns a non-nil SlashCommand
// when text starts with "/" followed by an alpha character.
func ParseSlashCommand(text string) *SlashCommand {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return nil
	}
	rest := strings.TrimPrefix(text, "/")
	if rest == "" {
		return nil
	}
	idx := strings.IndexAny(rest, " \t")
	name, args := rest, ""
	if idx >= 0 {
		name = rest[:idx]
		args = strings.TrimSpace(rest[idx+1:])
	}
	if name == "" {
		return nil
	}
	return &SlashCommand{Name: name, Args: args}
}

// SlashEntry describes one registered slash command for /help display.
type SlashEntry struct {
	Name string
	Desc string
	Args string
}

// CommandRegistry holds all registered slash commands.
type CommandRegistry struct {
	entries  map[string]SlashEntry
	handlers map[string]SlashHandler
}

// NewCommandRegistry creates a registry with built-in entries.
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{
		entries:  make(map[string]SlashEntry),
		handlers: make(map[string]SlashHandler),
	}
}

// Register adds a command to the registry.
func (cr *CommandRegistry) Register(name, desc, args string, handler SlashHandler) {
	cr.entries[name] = SlashEntry{Name: name, Desc: desc, Args: args}
	cr.handlers[name] = handler
}

// Handler returns the handler for a command, or nil if not found.
func (cr *CommandRegistry) Handler(name string) SlashHandler {
	return cr.handlers[name]
}

// Entries returns all registered entries for /help.
func (cr *CommandRegistry) Entries() []SlashEntry {
	out := make([]SlashEntry, 0, len(cr.entries))
	// Fixed order for deterministic output.
	order := []string{"help", "goal", "darwin", "council", "clear",
		"reflect", "compact", "status", "model", "models", "sandbox", "allow-all", "providers", "quit"}
	seen := make(map[string]struct{}, len(order))
	for _, name := range order {
		if e, ok := cr.entries[name]; ok {
			out = append(out, e)
			seen[name] = struct{}{}
		}
	}
	// Any extras not in the fixed order.
	for name, e := range cr.entries {
		if _, ok := seen[name]; ok {
			continue
		}
		out = append(out, e)
	}
	return out
}

// SafeWrap wraps a SlashHandler so that any panic is
// recovered and returned as an error instead of crashing
// the TUI. Every slash command MUST be wrapped.
func SafeWrap(name string, h SlashHandler) SlashHandler {
	return func(ctx context.Context, args string) (result string, err error) {
		if h == nil {
			return "", fmt.Errorf("%s: handler is not wired", name)
		}
		defer func() {
			if r := recover(); r != nil {
				result = ""
				err = fmt.Errorf("%s: panic: %v", name, r)
			}
		}()
		return h(ctx, args)
	}
}

// FormatHelp produces a formatted help view listing all
// registered slash commands. Simple text — no box-drawing.
func FormatHelp(entries []SlashEntry) string {
	var b strings.Builder
	b.WriteString("Commands:\n")
	for _, e := range entries {
		if e.Args != "" {
			fmt.Fprintf(&b, "  /%-14s %s  (%s)\n", e.Name, e.Desc, e.Args)
		} else {
			fmt.Fprintf(&b, "  /%-14s %s\n", e.Name, e.Desc)
		}
	}
	b.WriteString(helpKeys())
	return b.String()
}

// helpEssentials are the commands shown with descriptions in the
// short /help view, grouped by topic. Everything else is listed
// name-only on the "more" line; /help all shows full details.
var helpEssentials = []struct {
	group string
	names []string
}{
	{"models & providers", []string{"model", "models", "reasoning", "context-limit", "providers", "login"}},
	{"session", []string{"clear", "compact", "resume", "export", "cost", "status", "memory"}},
	{"agents", []string{"plan", "goal", "darwin", "council"}},
	{"system", []string{"settings", "doctor", "help", "quit"}},
}

// HelpContent returns the short, grouped help: the most important
// commands with descriptions, the rest as a compact list, and the
// key bindings. Use HelpContentAll for the full per-command list.
func HelpContent() string {
	return HelpContentFor("en")
}

// HelpContentFor returns the short help in the selected interface language.
func HelpContentFor(language string) string {
	byName := make(map[string]SlashEntry)
	for _, e := range HelpContentEntries() {
		if normalizeLanguage(language) == "pl" {
			e.Desc = polishCommandDescription(e.Name, e.Desc)
		}
		byName[e.Name] = e
	}
	shown := make(map[string]struct{})
	var b strings.Builder
	b.WriteString(textFor(language, "Commands:\n", "Polecenia:\n"))
	for _, g := range helpEssentials {
		group := g.group
		if normalizeLanguage(language) == "pl" {
			group = map[string]string{"models & providers": "modele i dostawcy", "session": "sesja", "agents": "agenci", "system": "system"}[group]
		}
		b.WriteString("\n " + group + "\n")
		for _, name := range g.names {
			e, ok := byName[name]
			if !ok {
				e = SlashEntry{Name: name}
			}
			shown[name] = struct{}{}
			if e.Args != "" {
				fmt.Fprintf(&b, "  /%-12s %s  (%s)\n", e.Name, e.Desc, e.Args)
			} else {
				fmt.Fprintf(&b, "  /%-12s %s\n", e.Name, e.Desc)
			}
		}
	}
	var rest []string
	for _, e := range HelpContentEntries() {
		if _, ok := shown[e.Name]; !ok {
			rest = append(rest, "/"+e.Name)
		}
	}
	if len(rest) > 0 {
		b.WriteString("\n " + textFor(language, "more: ", "więcej: ") + strings.Join(rest, " ") + "\n")
	}
	b.WriteString(textFor(language, " type /help all for every command with its description\n", " wpisz /help all, aby zobaczyć opisy wszystkich poleceń\n"))
	b.WriteString(helpKeysFor(language))
	return b.String()
}

// HelpContentAll returns the full per-command help list.
func HelpContentAll() string {
	return HelpContentAllFor("en")
}

// HelpContentAllFor returns full command help in the selected language.
func HelpContentAllFor(language string) string {
	entries := HelpContentEntries()
	if normalizeLanguage(language) == "pl" {
		for i := range entries {
			entries[i].Desc = polishCommandDescription(entries[i].Name, entries[i].Desc)
		}
	}
	if normalizeLanguage(language) != "pl" {
		return FormatHelp(entries)
	}
	var b strings.Builder
	b.WriteString("Polecenia:\n")
	for _, e := range entries {
		if e.Args != "" {
			fmt.Fprintf(&b, "  /%-14s %s  (%s)\n", e.Name, e.Desc, e.Args)
		} else {
			fmt.Fprintf(&b, "  /%-14s %s\n", e.Name, e.Desc)
		}
	}
	b.WriteString(helpKeysFor(language))
	return b.String()
}

func helpKeys() string {
	return helpKeysFor("en")
}

func helpKeysFor(language string) string {
	if normalizeLanguage(language) == "pl" {
		return "\nKlawisze: PgUp/PgDn przewijanie · Ctrl+C wyjście/przerwanie · Esc wyczyść pole\n" +
			"          Alt+Enter (lub Ctrl+J) nowa linia · Ctrl+Y kopiuj ostatnią odpowiedź · Ctrl+V wklej\n" +
			"          Ctrl+R poziom myślenia · Shift+T pokaż/ukryj myślenie · Shift+E rozwiń wynik narzędzia"
	}
	return "\nKeys: PgUp/PgDn scroll · Ctrl+C quit/cancel · Esc clear input · q types q\n" +
		"      Alt+Enter (or Ctrl+J) insert newline · Ctrl+Y copy last reply · Ctrl+V paste (keeps newlines)\n" +
		"      Ctrl+R open reasoning effort menu\n" +
		"      Shift+T toggle thinking · Shift+E expand tool output"
}

// formatSlashResult formats a command result for the transcript.
func formatSlashResult(name, body string) string {
	return fmt.Sprintf("_(%s)_ %s", name, body)
}
