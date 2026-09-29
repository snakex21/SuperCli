package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/ui/attachments"
	"supercli/internal/ui/desktopfiles"
)

type nativeAttachmentsMsg struct {
	paths []string
	err   error
}

func (m Model) openNativeAttachments() (tea.Model, tea.Cmd) {
	if !desktopfiles.Available() {
		return m.openAttachmentsMenu()
	}
	return m.beginNativeAttachments(desktopfiles.Pick)
}

func (m Model) beginNativeAttachments(pick func(string, string) ([]string, error)) (tea.Model, tea.Cmd) {
	if m.attachmentPickerOpen {
		return m, nil
	}
	m.attachmentPickerOpen = true
	dir := m.home
	if m.mode == modeMenu && m.menu.kind == menuAttachments {
		dir = m.menu.attachmentDir
	} else if len(m.pendingAttachments) > 0 {
		dir = filepath.Dir(m.pendingAttachments[len(m.pendingAttachments)-1])
	}
	language := m.language
	m.setStatus(m.tr("tui.model_attachments_native.571af06600"), true)
	return m, func() tea.Msg {
		paths, err := pick(dir, language)
		return nativeAttachmentsMsg{paths: paths, err: err}
	}
}

func (m Model) applyNativeAttachments(msg nativeAttachmentsMsg) (tea.Model, tea.Cmd) {
	m.attachmentPickerOpen = false
	m.setStatus("", true)
	if msg.err != nil {
		next, cmd := m.openAttachmentsMenu()
		m = next.(Model)
		m.menu.formErr = m.tr("tui.model_attachments_native.1ba1f53308") + msg.err.Error()
		return m, cmd
	}
	if len(msg.paths) == 0 {
		return m, nil
	} // Cancel leaves draft and selection intact.
	next, cmd := m.applyAttachmentSelection(msg.paths)
	m = next.(Model)
	if m.statusSuccess && m.mode == modeMenu && m.menu.kind == menuAttachments {
		closed, _ := m.closeMenu()
		m = closed.(Model)
	}
	return m, cmd
}

// Add a whole selection or leave the current draft untouched. Repeated paste
// adds no duplicate and never toggles a previously selected file off.
func (m *Model) addAttachmentPaths(paths []string) (int, error) {
	selected := append([]string(nil), m.pendingAttachments...)
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return 0, fmt.Errorf(m.tr("tui.model_attachments_native.b6363d5c07"), path)
		}
		path = filepath.Clean(path)
		duplicate := false
		for _, existing := range selected {
			if sameAttachmentPath(existing, path) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			selected = append(selected, path)
		}
	}
	if len(selected) > attachments.MaxFiles {
		return 0, fmt.Errorf(m.tr("tui.menu_attachments.e2dd0e64b3"), attachments.MaxFiles)
	}
	var total int64
	for _, path := range selected {
		info, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		if !info.Mode().IsRegular() {
			return 0, fmt.Errorf(m.tr("tui.model_attachments_native.c67733d9d6"), filepath.Base(path))
		}
		if info.Size() > attachments.MaxFileBytes {
			return 0, fmt.Errorf(m.tr("tui.model_attachments_native.c20cb50356"), filepath.Base(path))
		}
		total += info.Size()
	}
	if total > attachments.MaxTotalBytes {
		return 0, errors.New(m.tr("tui.model_attachments_native.edf3b60a1a"))
	}
	added := len(selected) - len(m.pendingAttachments)
	m.pendingAttachments = selected
	return added, nil
}

func (m Model) applyAttachmentSelection(paths []string) (tea.Model, tea.Cmd) {
	added, err := m.addAttachmentPaths(paths)
	if err != nil {
		m.setStatus(m.tr("tui.model_attachments_native.623c4ebb74")+err.Error(), false)
		if m.mode == modeMenu && m.menu.kind == menuAttachments {
			m.menu.formErr = err.Error()
		}
	} else {
		m.menu.formErr = ""
		m.setStatus(fmt.Sprintf(m.tr("tui.model_attachments_native.5831f6a18d"), added), true)
		m.syncInputHeight()
	}
	return m, m.statusClearCmd()
}

func (m Model) pasteClipboard() (tea.Model, tea.Cmd) {
	paths, err := desktopfiles.ClipboardFiles(attachments.MaxFiles)
	if err != nil {
		m.setStatus(m.tr("tui.model_attachments_native.f95ed9dbe3")+err.Error(), false)
		return m, m.statusClearCmd()
	}
	if len(paths) > 0 {
		if m.busy {
			m.setStatus(m.tr("tui.model_attachments_native.e4bb8d7796"), false)
			return m, m.statusClearCmd()
		}
		return m.applyAttachmentSelection(paths)
	}
	if desktopfiles.HasClipboardImage() {
		return m.pasteClipboardImage()
	}
	if text, err := clipboard.ReadAll(); err == nil && text != "" {
		if m.mode == modeMenu && m.menu.kind == menuAttachments {
			m.menu.filter += normalizePastedLine(text)
			m.menu.cursor = 0
			return m, nil
		}
		m.input.InsertString(normalizePastedText(text))
		m.syncInputHeight()
		if !m.busy {
			m.updateAutocompleteState()
		}
	}
	return m, nil
}
