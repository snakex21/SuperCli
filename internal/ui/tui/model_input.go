// Package tui is the Bubble Tea presentation layer. F25 replaces
// the raw transcript with a structured chat view (role-based
// colors), adds a status bar, inline event markers, a tool-
// name spinner, Ctrl+C run cancellation, and PgUp/PgDn scrolling.
package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/system/config"
	"supercli/internal/tools"
)

func (m Model) handleBusyInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "enter" {
		if m.cancelling {
			m.setStatus(m.tr("tui.model_input.c29bf4d47d"), false)
			return m, m.statusClearCmd()
		}
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		q, ok := m.agent.(interjectionQueuer)
		if !ok || !q.QueueInterjection(text) {
			m.setStatus(m.tr("tui.model_input.9882c2b0ed"), false)
			return m, m.statusClearCmd()
		}
		m.chat.addUser("> " + text)
		m.appendLineToTranscript("> " + text)
		m.appendLine(m.palette.InputHint.Render(m.tr("tui.model_input.d5f42b0f2b")))
		m.input.Reset()
		m.syncInputHeight()
		m.refreshTranscript()
		return m, nil
	}
	if msg.String() == "ctrl+v" {
		return m.pasteClipboard()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.syncTypedInputHeight()
	return m, cmd
}

// Ordinary typing only changes transcript geometry when the input gains or
// loses a visible row. Runtime/attachment transitions keep syncInputHeight's
// complete resize path; cursor movement must not rebuild their layout.
func (m *Model) syncTypedInputHeight() {
	h := min(max(1, m.input.LineCount()), maxInputLines)
	if m.input.Height() != h {
		m.syncInputHeight()
	}
}

// handleCtrlC implements the F25 cancel behavior:
// - If busy: cancel the current agent run (not the process).
// - If asking: cancel the ask.
// - If idle: quit the program. Single-letter keys like q do not quit.
func (m Model) handleCtrlC() (tea.Model, tea.Cmd) {
	if m.mode == modeAsking {
		if m.pendingAsk != nil {
			safeRespond(m.pendingAsk.respond, tools.AskAnswer{Cancelled: true})
		}
		m.endAsk()
		return m, nil
	}
	if m.cancelling {
		m.quitting = true
		return m, tea.Quit
	}
	if m.busy {
		// Cancel the active run. (This used to append the
		// "running" marker, which read as if the run was
		// still in progress after cancelling.)
		m.cancel.Cancel()
		m.resumeContext = nil
		m.cancelling = m.eventCh != nil || m.submittingDraft != ""
		m.busy = m.cancelling
		m.cancel.Disarm()
		m.setStatus("cancelled", true)
		if m.cancelling {
			m.setStatus(m.tr("tui.model_input.bbe8574175"), true)
		}
		m.appendLine(m.palette.InputHint.Render(m.tr("tui.model_input.875d9607de")))
		m.refreshTranscript()
		return m, m.statusClearCmd()
	}
	// Idle → quit.
	m.quitting = true
	return m, tea.Quit
}

// handleEscCancel cancels the current agent run without quitting
// the program. Shows "cancelled" in the status bar for 2 seconds.
// Unlike Ctrl+C (which quits when idle), ESC only acts while busy.
func (m Model) handleEscCancel() (tea.Model, tea.Cmd) {
	if !m.busy {
		return m, nil
	}
	m.cancel.Cancel()
	m.resumeContext = nil
	m.cancelling = m.eventCh != nil || m.submittingDraft != ""
	m.busy = m.cancelling
	m.cancel.Disarm()
	m.setStatus("cancelled", true)
	if m.cancelling {
		m.setStatus(m.tr("tui.model_input.bbe8574175"), true)
	}
	m.appendLine(m.palette.InputHint.Render(m.tr("tui.model_input.5d38ad7b5a")))
	m.refreshTranscript()
	// Clear the override after 2 seconds.
	return m, m.statusClearCmd()
}

// handleKey processes key events when the TUI is idle.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+k":
		return m.openActionsMenu()
	case "ctrl+o":
		return m.openNativeAttachments()
	case "ctrl+v":
		return m.pasteClipboard()
	}
	// Autocomplete popup navigation — intercept keys BEFORE scroll.
	if m.autocomp.kind != autocompNone {
		return m.handleAutocompleteKey(msg)
	}

	// F25: scroll keys are handled next. When the input box
	// holds multiple lines, arrows/home/end move the cursor
	// inside the textarea instead of scrolling the chat.
	if !(m.input.LineCount() > 1 && isInputNavKey(msg.String())) {
		if HandleScroll(&m.viewport, msg, m.scroll) {
			return m, nil
		}
	}

	switch msg.String() {
	case "esc":
		if m.input.Value() != "" {
			m.input.Reset()
			m.syncTypedInputHeight()
			return m, nil
		}
		// Empty input + Esc is a no-op. The exit tip is shown
		// only once, and only when the user types a bare
		// quit-like word (see the "enter" case below).
		return m, nil
	case "T":
		m.chat.toggleThinking()
		m.refreshTranscript()
		return m, nil
	case "E":
		m.toolExpanded = !m.toolExpanded
		m.refreshTranscript()
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		m.input.Reset()
		m.syncInputHeight()
		return m.startPrompt(text)
	case "ctrl+r":
		return m.openReasoningMenu()
	case "ctrl+f":
		return m.openTranscriptSearchMenu()
	case "tab":
		// Empty-input Tab is the discoverable, GUI-like entry point to
		// common actions. A non-empty input keeps the textarea's normal
		// Tab behaviour; slash/@ autocomplete owns Tab while it is open.
		if strings.TrimSpace(m.input.Value()) == "" {
			return m.openActionsMenu()
		}
	case "ctrl+p":
		return m.openProjectsMenu()
	case "ctrl+y":
		// Copy the last assistant response to the clipboard.
		last := m.chat.lastAssistant()
		if last == "" {
			m.setStatus(m.tr("tui.model_input.afd6f64e92"), false)
		} else if err := clipboard.WriteAll(last); err != nil {
			m.setStatus(fmt.Sprintf(m.tr("tui.model_input.7e6196ef1a"), err), false)
		} else {
			m.setStatus(m.tr("tui.model_input.15d4a01b74"), true)
		}
		return m, m.statusClearCmd()
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.syncTypedInputHeight()
	m.updateAutocompleteState()
	return m, cmd
}

// persistReasoningEffort updates the active configuration as well as the
// global default. A project override must not restore an older level on restart.
func (m *Model) persistReasoningEffort(level string) {
	if m.providerMgr != nil {
		if err := m.providerMgr.SaveReasoningEffort(level); err != nil {
			m.setStatus(fmt.Sprintf(m.tr("tui.model_input.fc719769ad"), err), false)
		}
		return
	}
	cwd, _ := os.Getwd()
	globalPath, _ := config.FindTomlPaths(m.dataDir, cwd)
	if tc, err := config.LoadToml(globalPath); err == nil {
		tc.ReasoningEffort = level
		if err := config.SaveToml(globalPath, tc); err != nil {
			m.setStatus(fmt.Sprintf(m.tr("tui.model_input.fc719769ad"), err), false)
		}
	}
}

// isInputNavKey reports whether the key is one the multi-line
// input needs for in-box cursor movement.
func isInputNavKey(s string) bool {
	switch s {
	case "up", "down", "home", "end":
		return true
	}
	return false
}

func shouldIgnoreAltKey(msg tea.KeyMsg) bool {
	if !msg.Alt || msg.Paste {
		return false
	}
	// Alt+Enter inserts a newline in the multi-line input.
	if msg.Type == tea.KeyEnter {
		return false
	}
	if len(msg.Runes) == 0 {
		return true
	}
	for _, r := range msg.Runes {
		if r > 127 {
			return false
		}
	}
	return true
}

// normalizePastedText prepares clipboard text for the
// multi-line chat input: newlines are PRESERVED (pasted
// code keeps its formatting), line endings are normalized
// to \n, and control characters are stripped. The Windows
// clipboard is UTF-16 and a bad conversion can leak NUL
// bytes into the pasted text; persisting those corrupts
// config.toml fields such as provider API keys.
func normalizePastedText(text string) string {
	text = strings.TrimRight(text, "\r\n")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	// Drop remaining control characters (NUL, ESC, ...) but
	// keep tabs and newlines.
	text = strings.Map(func(r rune) rune {
		if r != '\t' && r != '\n' && (r < 0x20 || r == 0x7f) {
			return -1
		}
		return r
	}, text)
	return text
}

// normalizePastedLine is the single-line variant used by
// form fields (menu inputs): like normalizePastedText, but
// newlines collapse to single spaces.
func normalizePastedLine(text string) string {
	return strings.ReplaceAll(normalizePastedText(text), "\n", " ")
}
