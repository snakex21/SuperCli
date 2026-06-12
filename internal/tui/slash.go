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
		"reflect", "compact", "status", "model", "sandbox", "providers", "quit"}
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
	{"models & providers", []string{"model", "reasoning", "providers", "login"}},
	{"session", []string{"clear", "compact", "resume", "export", "cost", "status", "memory"}},
	{"agents", []string{"plan", "goal", "darwin", "council"}},
	{"system", []string{"doctor", "help", "quit"}},
}

// HelpContent returns the short, grouped help: the most important
// commands with descriptions, the rest as a compact list, and the
// key bindings. Use HelpContentAll for the full per-command list.
func HelpContent() string {
	byName := make(map[string]SlashEntry)
	for _, e := range HelpContentEntries() {
		byName[e.Name] = e
	}
	shown := make(map[string]struct{})
	var b strings.Builder
	b.WriteString("Commands:\n")
	for _, g := range helpEssentials {
		b.WriteString("\n " + g.group + "\n")
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
		b.WriteString("\n more: " + strings.Join(rest, " ") + "\n")
	}
	b.WriteString(" type /help all for every command with its description\n")
	b.WriteString(helpKeys())
	return b.String()
}

// HelpContentAll returns the full per-command help list.
func HelpContentAll() string {
	return FormatHelp(HelpContentEntries())
}

func helpKeys() string {
	return "\nKeys: PgUp/PgDn scroll · Ctrl+C quit/cancel · Esc clear input · q types q\n" +
		"      Alt+Enter (or Ctrl+J) insert newline · Ctrl+Y copy last reply · Ctrl+V paste (keeps newlines)\n" +
		"      Shift+T toggle thinking · Shift+E expand tool output"
}

// formatSlashResult formats a command result for the transcript.
func formatSlashResult(name, body string) string {
	return fmt.Sprintf("_(%s)_ %s", name, body)
}
