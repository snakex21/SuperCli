package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/ui/attachments"
)

type attachmentEntry struct {
	path, name string
	dir        bool
}
type attachmentDirectoryMsg struct {
	path    string
	entries []attachmentEntry
	err     error
}

func (m Model) openAttachmentsMenu() (tea.Model, tea.Cmd) {
	m.enterMenu(interactiveMenu{kind: menuAttachments, attachmentDir: m.home, attachmentLoading: true})
	return m, readAttachmentDirectory(m.home)
}
func readAttachmentDirectory(path string) tea.Cmd {
	return func() tea.Msg {
		list, err := os.ReadDir(path)
		result := attachmentDirectoryMsg{path: path, err: err}
		for _, entry := range list {
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			result.entries = append(result.entries, attachmentEntry{path: filepath.Join(path, entry.Name()), name: entry.Name(), dir: entry.IsDir()})
		}
		sort.SliceStable(result.entries, func(i, j int) bool {
			a, b := result.entries[i], result.entries[j]
			if a.dir != b.dir {
				return a.dir
			}
			return strings.ToLower(a.name) < strings.ToLower(b.name)
		})
		return result
	}
}
func (m Model) attachmentRows() []attachmentEntry {
	if m.menu.category == 1 {
		rows := make([]attachmentEntry, 0, len(m.pendingAttachments))
		for _, path := range m.pendingAttachments {
			rows = append(rows, attachmentEntry{path: path, name: filepath.Base(path)})
		}
		return rows
	}
	var rows []attachmentEntry
	for _, e := range m.menu.attachmentEntries {
		if strings.Contains(strings.ToLower(e.name), strings.ToLower(m.menu.filter)) {
			rows = append(rows, e)
		}
	}
	return rows
}
func sameAttachmentPath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	return a == b || (filepath.Separator == '\\' && strings.EqualFold(a, b))
}

func (m *Model) toggleAttachment(path string) {
	for i, p := range m.pendingAttachments {
		if sameAttachmentPath(p, path) {
			m.pendingAttachments = append(m.pendingAttachments[:i], m.pendingAttachments[i+1:]...)
			m.menu.formErr = ""
			return
		}
	}
	if len(m.pendingAttachments) >= attachments.MaxFiles {
		m.menu.formErr = fmt.Sprintf(m.tr("tui.menu_attachments.e2dd0e64b3"), attachments.MaxFiles)
		return
	}
	m.pendingAttachments = append(m.pendingAttachments, path)
	m.menu.formErr = ""
}
func (m Model) attachmentSelected(path string) bool {
	for _, p := range m.pendingAttachments {
		if sameAttachmentPath(p, path) {
			return true
		}
	}
	return false
}
func (m Model) changeAttachmentDir(path string) (tea.Model, tea.Cmd) {
	path = strings.Trim(strings.TrimSpace(path), "'\"")
	if !filepath.IsAbs(path) {
		path = filepath.Join(m.menu.attachmentDir, path)
	}
	m.menu.attachmentDir = filepath.Clean(path)
	m.menu.attachmentLoading = true
	m.menu.attachmentEntries = nil
	m.menu.filter = ""
	m.menu.cursor = 0
	m.menu.formErr = ""
	return m, readAttachmentDirectory(m.menu.attachmentDir)
}
func (m Model) handleAttachmentsKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.String() == "ctrl+o" {
		return m.openNativeAttachments()
	}
	if m.menu.editing {
		switch key.String() {
		case "esc":
			m.menu.editing = false
			return m, nil
		case "enter":
			m.menu.editing = false
			return m.changeAttachmentDir(m.menu.editBuf)
		case "backspace", "ctrl+h":
			r := []rune(m.menu.editBuf)
			if len(r) > 0 {
				m.menu.editBuf = string(r[:len(r)-1])
			}
			return m, nil
		case "ctrl+u":
			m.menu.editBuf = ""
			return m, nil
		case "ctrl+v":
			if value, err := clipboard.ReadAll(); err == nil {
				m.menu.editBuf += normalizePastedLine(value)
			}
			return m, nil
		}
		if len(key.Runes) > 0 {
			m.menu.editBuf += normalizePastedLine(string(key.Runes))
		}
		return m, nil
	}
	switch key.String() {
	case "ctrl+v":
		return m.pasteClipboard()
	case "ctrl+l":
		m.menu.editing = true
		m.menu.editBuf = ""
		return m, nil
	case "left", "right", "tab", "shift+tab":
		m.menu.category = 1 - m.menu.category
		m.menu.filter = ""
		m.menu.cursor = 0
		return m, nil
	case "ctrl+backspace":
		return m.changeAttachmentDir(filepath.Dir(m.menu.attachmentDir))
	case " ":
		return m.selectAttachment()
	case "delete":
		rows := m.attachmentRows()
		if m.menu.cursor >= 2 && m.menu.cursor-2 < len(rows) {
			e := rows[m.menu.cursor-2]
			if m.attachmentSelected(e.path) {
				m.toggleAttachment(e.path)
			}
		}
		m.menu.cursor = minInt(m.menu.cursor, len(m.attachmentRows())+1)
		return m, nil
	}
	return m.handleSearchMenuKey(key, func() int { return len(m.attachmentRows()) + 2 }, m.selectAttachment)
}
func (m Model) selectAttachment() (tea.Model, tea.Cmd) {
	if m.menu.cursor == 0 {
		return m.closeMenu()
	}
	if m.menu.cursor == 1 {
		return m.changeAttachmentDir(filepath.Dir(m.menu.attachmentDir))
	}
	rows := m.attachmentRows()
	i := m.menu.cursor - 2
	if i < 0 || i >= len(rows) {
		return m, nil
	}
	entry := rows[i]
	if entry.dir {
		return m.changeAttachmentDir(entry.path)
	}
	m.toggleAttachment(entry.path)
	m.menu.cursor = minInt(m.menu.cursor, len(m.attachmentRows())+1)
	return m, nil
}
func (m Model) renderAttachmentsMenu() string {
	p := menuPage{title: fmt.Sprintf(m.tr("tui.menu_attachments.c4d916d8f3"), len(m.pendingAttachments), attachments.MaxFiles),
		subtitle: m.menu.attachmentDir, searchable: !m.menu.editing,
		tabs:        m.menuTabs([]string{m.tr("tui.autocomplete_render.abc7e98928"), m.tr("tui.menu_attachments.57fd7a0cf3")}, m.menu.category),
		footer:      m.tr("tui.menu_attachments.1d1c29e672"),
		detailTitle: m.tr("tui.menu_attachments.bcbdd23083"),
		detail:      []string{m.tr("tui.menu_attachments.e21aabc940"), "", m.tr("tui.menu_attachments.e34925d124")}}
	p.items = []menuListItem{{label: m.tr("tui.menu_attachments.c80edd86d3"), badge: fmt.Sprint(len(m.pendingAttachments))}, {label: "..", meta: m.tr("tui.menu_attachments.79498d0401")}}
	for _, e := range m.attachmentRows() {
		badge := "[ ]"
		if e.dir {
			badge = m.tr("tui.menu_attachments.034a006248")
		} else if m.attachmentSelected(e.path) {
			badge = "[x]"
		}
		p.items = append(p.items, menuListItem{label: e.name, badge: badge})
	}
	if m.menu.attachmentLoading {
		p.subtitle = m.tr("tui.menu_attachments.b132667b81") + m.menu.attachmentDir
	}
	if m.menu.editing {
		p.subtitle = m.tr("tui.menu_attachments.95ccec987d") + m.menu.editBuf + "|"
		p.footer = m.tr("tui.menu_attachments.647cc445e0")
		// Keep the editable path visible even in a short terminal.
		p.tabs = p.subtitle
	}
	if m.menu.cursor >= 2 {
		rows := m.attachmentRows()
		if i := m.menu.cursor - 2; i < len(rows) {
			p.detail = append(p.detail, "", rows[i].path)
		}
	}
	return m.renderMenuPage(p)
}
func attachmentDisplay(paths []string, languages ...string) string {
	language := "en"
	if len(languages) > 0 {
		language = languages[0]
	}
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		names = append(names, filepath.Base(path))
	}
	return textFor(language, "tui.other.513ba0d0e5") + strings.Join(names, ", ")
}
