// Package tui is the Bubble Tea presentation layer. F25 replaces
// the raw transcript with a structured chat view (role-based
// colors), adds a status bar, inline event markers, a tool-
// name spinner, Ctrl+C run cancellation, and PgUp/PgDn scrolling.
package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/agent"
)

func isQuitCommand(text string) bool {
	s := strings.ToLower(strings.TrimSpace(text))
	return s == "/quit" || s == "/exit"
}

// waitForEvent returns a tea.Cmd that reads one event from ch
// and re-emits it as a tea.Msg.
func waitForEvent(ch <-chan agent.Event) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return runEndMsg{}
		}
		return runEventMsg{ev: ev}
	}
}

// waitForNextEvent returns a tea.Cmd that reads the next event
// from m.eventCh. Returns nil when the run has ended (eventCh is
// nil). This is the correct continuation after a non-terminal
// agent event (MessageEvent, ToolCallEvent, etc.).
func (m *Model) waitForNextEvent() tea.Cmd {
	return waitForEvent(m.eventCh)
}

// streamFlushCmd returns a tea.Cmd that sleeps 16ms then emits
// streamFlushMsg. This forces Bubble Tea to call View() between
// rapid-fire MessageEvents so the user sees text appear live
// (token-by-token streaming) instead of all at once on DoneEvent.
func streamFlushCmd() tea.Cmd {
	return func() tea.Msg {
		time.Sleep(16 * time.Millisecond)
		return streamFlushMsg{}
	}
}

// waitForExternalEvent is the F12 companion for the external sink.
func waitForExternalEvent(ch <-chan agent.Event) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return runExtEventMsg{ev: ev}
	}
}

// nil_to_ctx is a tiny helper. Kept for backward compat with
// any callers that still use it; new code should pass ctx.
func nil_to_ctx() context.Context { return context.Background() }

// dispatchSlashCommand runs a slash command handler in a
// goroutine and returns a tea.Cmd that emits a
// slashResultMsg when the handler is done.
