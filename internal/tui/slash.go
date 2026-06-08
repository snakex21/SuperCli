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
		"reflect", "compact", "status", "models", "sandbox", "providers", "quit"}
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
	b.WriteString("\nKeys: PgUp/PgDn scroll · Ctrl+C quit/cancel · Esc clear input · q types q")
	return b.String()
}

// HelpContent returns the hardcoded help text.
func HelpContent() string {
	return FormatHelp([]SlashEntry{
		{Name: "help", Desc: "show this help message"},
		{Name: "goal", Desc: "manage active goal", Args: "<set|list|show|tasks|done> [args]"},
		{Name: "darwin", Desc: "run N parallel agents, pick best", Args: "[N] <prompt>"},
		{Name: "council", Desc: "sample N cheap models, judge picks winner", Args: "[N] <prompt>"},
		{Name: "clear", Desc: "hide recent messages from model context"},
		{Name: "reflect", Desc: "show learned patterns from reflection"},
		{Name: "compact", Desc: "compress context to save tokens"},
		{Name: "status", Desc: "show credits and session info"},
		{Name: "models", Desc: "list available models"},
		{Name: "sandbox", Desc: "show sandbox status"},
		{Name: "plan", Desc: "toggle plan mode (read-only analysis)"},
		{Name: "diff", Desc: "show file changes from current session"},
		{Name: "model", Desc: "show or swap active model", Args: "[model_id]"},
		{Name: "resume", Desc: "resume a previous session", Args: "[session_id]"},
		{Name: "export", Desc: "export session to Markdown file", Args: "[filename.md]"},
		{Name: "cost", Desc: "show cost dashboard with per-turn breakdown"},
		{Name: "undo", Desc: "revert last file write/edit operations", Args: "[N]"},
		{Name: "providers", Desc: "manage providers and model prices", Args: "[add|remove|price|toggle] [args]"},
		{Name: "quit", Desc: "exit SuperCli explicitly"},
	})
}

// formatSlashResult formats a command result for the transcript.
func formatSlashResult(name, body string) string {
	return fmt.Sprintf("_(%s)_ %s", name, body)
}
