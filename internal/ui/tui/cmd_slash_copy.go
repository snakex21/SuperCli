package tui

import (
	"fmt"
	"os"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
)

// Clipboard writers are variables so tests never touch the real clipboard
// or terminal. The native path (Win32 CF_UNICODETEXT on Windows, pbcopy /
// xclip / wl-copy elsewhere) is tried first; OSC 52 asks the terminal to set
// the clipboard and also works over SSH.
var (
	copyNativeClipboard = clipboard.WriteAll
	copyOSC52Clipboard  = func(text string) error {
		termenv.NewOutput(os.Stdout).Copy(text)
		return nil
	}
)

// copyToClipboard reports which path succeeded: "native" or "osc52".
func copyToClipboard(text string) (string, error) {
	nativeErr := copyNativeClipboard(text)
	if nativeErr == nil {
		return "native", nil
	}
	if err := copyOSC52Clipboard(text); err != nil {
		return "", fmt.Errorf("%v; OSC 52: %w", nativeErr, err)
	}
	return "osc52", nil
}

// handleSlashCopy copies the last assistant reply (same source as Ctrl+Y).
func (m Model) handleSlashCopy(cmd SlashCommand) (tea.Model, tea.Cmd) {
	last := m.chat.lastAssistant()
	if last == "" {
		m.setStatus(m.tr("tui.model_input.afd6f64e92"), false)
		return m, m.statusClearCmd()
	}
	path, err := copyToClipboard(last)
	switch {
	case err != nil:
		m.setStatus(fmt.Sprintf(m.tr("tui.model_input.7e6196ef1a"), err), false)
	case path == "native":
		m.setStatus(m.tr("tui.copy.native"), true)
	default:
		m.setStatus(m.tr("tui.copy.osc52"), true)
	}
	return m, m.statusClearCmd()
}
