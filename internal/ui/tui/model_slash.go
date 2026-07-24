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
	if cmd.Name == "resume" && m.commands["resume"] == nil {
		return m.handleSlashResumeBuiltin(cmd)
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
			return slashResultMsg{Body: out, Err: err}
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
		return slashResultMsg{Body: out, Err: err}
	}
}

func (m Model) handleSlashResumeBuiltin(cmd SlashCommand) (tea.Model, tea.Cmd) {
	store := m.sessionStore
	dm := m.marker
	chat := m.chat
	args := cmd.Args
	if store == nil {
		return m, func() tea.Msg {
			return slashResultMsg{Body: dm.Diff("/resume: session store not available")}
		}
	}
	if args == "" {
		return m, func() tea.Msg {
			sessions, err := store.List(10)
			if err != nil {
				return slashResultMsg{Err: fmt.Errorf("resume list: %w", err)}
			}
			if len(sessions) == 0 {
				return slashResultMsg{Body: dm.Diff("No previous sessions found.")}
			}
			var b strings.Builder
			b.WriteString(fmt.Sprintf("_[%d recent session(s)]_\n\n", len(sessions)))
			for _, s := range sessions {
				title := s.Title
				if title == "" {
					title = "(untitled)"
				}
				b.WriteString(fmt.Sprintf("  %s  %s  [%d msgs, %s]\n",
					s.ID[:8], title, s.MessageCount,
					s.UpdatedAt.Format("2006-01-02 15:04")))
			}
			b.WriteString("\nUsage: /resume <session_id>")
			return slashResultMsg{Body: dm.Diff(b.String())}
		}
	}
	target := strings.TrimSpace(args)
	return m, func() tea.Msg {
		sess, err := store.Get(target)
		if err != nil {
			return slashResultMsg{Err: fmt.Errorf("resume %s: %w", target, err)}
		}
		msgs, err := store.ReadMessages(context.Background(), target)
		if err != nil {
			return slashResultMsg{Err: fmt.Errorf("resume read %s: %w", target, err)}
		}
		title := sess.Title
		if title == "" {
			title = "(untitled)"
		}
		chat.addAssistant(fmt.Sprintf("Resumed session %s: %s (%d messages)",
			sess.ID[:8], title, len(msgs)))
		for _, msg := range msgs {
			if msg.Role == "user" {
				chat.addUser(msg.Content)
			} else if msg.Role == "assistant" {
				chat.addAssistant(msg.Content)
			}
		}
		return slashResultMsg{Body: dm.ModelInfo(fmt.Sprintf("loaded %d messages from session %s", len(msgs), sess.ID[:8]))}
	}
}
