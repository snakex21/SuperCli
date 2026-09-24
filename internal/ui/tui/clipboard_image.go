package tui

import (
	"fmt"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/ui/attachments"
	"supercli/internal/ui/desktopfiles"
)

type clipboardImageMsg struct {
	path string
	err  error
}

func (m Model) pasteClipboardImage() (tea.Model, tea.Cmd) {
	if m.busy {
		m.setStatus(m.tr("Paste the image after the current response finishes", "Wklej obraz po zakończeniu bieżącej odpowiedzi"), false)
		return m, m.statusClearCmd()
	}
	m.attachmentPickerOpen = true
	m.setStatus(m.tr("Pasting image…", "Wklejanie obrazu…"), true)
	dir := m.dataDir
	if dir == "" && m.home != "" {
		dir = filepath.Join(m.home, ".supercli")
	}
	return m, func() tea.Msg {
		data, err := desktopfiles.ClipboardPNG()
		if err != nil {
			return clipboardImageMsg{err: err}
		}
		path, err := desktopfiles.SaveClipboardPNG(dir, data)
		return clipboardImageMsg{path: path, err: err}
	}
}
func (m Model) finishClipboardImage(msg clipboardImageMsg) (tea.Model, tea.Cmd) {
	m.attachmentPickerOpen = false
	if msg.err != nil {
		m.setStatus(fmt.Sprintf(m.tr("Could not paste image: %v", "Nie udało się wkleić obrazu: %v"), msg.err), false)
		return m, m.statusClearCmd()
	}
	return m.applyAttachmentSelection([]string{msg.path})
}

func (m Model) pasteClipboardAttachments() (tea.Model, tea.Cmd) {
	paths, err := desktopfiles.ClipboardFiles(attachments.MaxFiles)
	if err != nil {
		m.setStatus(err.Error(), false)
		return m, m.statusClearCmd()
	}
	if len(paths) > 0 {
		return m.applyAttachmentSelection(paths)
	}
	if desktopfiles.HasClipboardImage() {
		return m.pasteClipboardImage()
	}
	m.setStatus(m.tr("Copy an image or file first", "Najpierw skopiuj obraz lub plik"), false)
	return m, m.statusClearCmd()
}
