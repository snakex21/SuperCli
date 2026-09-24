// Package tui is the Bubble Tea presentation layer. F25 replaces
// the raw transcript with a structured chat view (role-based
// colors), adds a status bar, inline event markers, a tool-
// name spinner, Ctrl+C run cancellation, and PgUp/PgDn scrolling.
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) dispatchSlashCommand(cmd SlashCommand) (tea.Model, tea.Cmd) {
	if cmd.Name == "quit" || cmd.Name == "exit" {
		m.quitting = true
		return m, tea.Quit
	}
	// Bare menu openers — keep as one-liners so the dispatcher stays
	// the single place that decides menu vs text-path.
	if cmd.Name == "providers" && cmd.Args == "" {
		return m.openProvidersMenu()
	}
	if cmd.Name == "attach" {
		return m.openAttachmentsMenu()
	}
	if cmd.Name == "settings" {
		return m.openSettingsMenu()
	}
	if cmd.Name == "models" && cmd.Args == "" {
		return m.openModelCatalogMenu()
	}
	if cmd.Name == "goal" && cmd.Args == "" {
		return m.openGoalMenu()
	}
	if cmd.Name == "projects" && cmd.Args == "" {
		return m.openProjectsMenu()
	}

	// Named builtins (see slash_handlers.go). /providers with args
	// falls through here; bare form was handled above.
	if handler, ok := m.slashBuiltin(cmd.Name); ok {
		// /model with empty args opens the menu (handled above via
		// models catalog only for /models). /model bare opens picker.
		if cmd.Name == "model" && cmd.Args == "" {
			return m.openModelsMenu()
		}
		// /providers bare already returned; with args use the text path.
		return handler(m, cmd)
	}

	// F26.6: /resume lists or resumes previous sessions.
	// When main.go wires a real resume handler (wave 4: loads
	// the session into the agent loop, summarizing oversized
	// history), it takes precedence; this builtin is the
	// transcript-only fallback.
	if cmd.Name == "resume" {
		if cmd.Args == "" {
			return m.openSessionsMenu()
		}
		if !strings.EqualFold(cmd.Args, "all") {
			return m.resumeConversation(strings.TrimSpace(cmd.Args))
		}
	}

	handler, ok := m.commands[cmd.Name]
	if !ok {
		m.appendLine(formatSlashResult("unknown command", fmt.Sprintf("`/%s` is not registered. %s", cmd.Name, RenderHelp())))
		m.refreshTranscript()
		return m, nil
	}
	handler = SafeWrap(cmd.Name, handler)
	if !cmd.Quiet {
		m.chat.addUser("> /" + cmd.Name + " " + cmd.Args)
		m.appendLineToTranscript("> /" + cmd.Name + " " + cmd.Args)
	}
	if localSlashCommands[cmd.Name] {
		// Fast local commands (no LLM/network work) must not flip
		// the TUI into the busy/running state: no "running ·
		// Ctrl+C to abort" marker, no working spinner. The handler
		// still runs as a tea.Cmd so a slow disk never freezes the
		// UI; slashResultMsg renders the result.
		m.refreshTranscript()
		return m, func() tea.Msg {
			out, err := handler(context.Background(), cmd.Args)
			return slashResultMsg{Body: out, Err: err, Document: true, Local: true}
		}
	}
	m.appendLine(m.marker.Running())
	m.refreshTranscript()
	m.busy = true
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel.Arm(cancelRun, cancel)
	return m, func() tea.Msg {
		defer cancel()
		out, err := handler(ctx, cmd.Args)
		return slashResultMsg{Body: out, Err: err, Document: true}
	}
}
