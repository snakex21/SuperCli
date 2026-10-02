package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/system/browser"
	"supercli/internal/system/childproc"
)

type previewPasteMsg struct {
	id   uint64
	text string
	err  error
}

type previewOpenedMsg struct {
	id            uint64
	url           string
	systemBrowser bool
	unavailable   bool
	err           error
}

func (m Model) openPreviewMenu() (tea.Model, tea.Cmd) {
	input := textinput.New()
	input.Prompt = ""
	input.CharLimit = 4096
	input.Placeholder = "http://localhost:3000"
	// A static cursor avoids adding another timer to an active agent run.
	input.Cursor.SetMode(cursor.CursorStatic)
	input.TextStyle = m.palette.StatusValue
	input.PlaceholderStyle = m.palette.Dim
	input.Cursor.Style = m.palette.HeaderMode
	input.SetValue(m.previewURL)
	input.CursorEnd()
	input.Focus()
	m.previewID++
	m.enterMenu(interactiveMenu{kind: menuPreview, previewInput: input, previewID: m.previewID})
	return m, nil
}

func (m *Model) focusPreviewAddress() {
	if m.menu.cursor == 0 {
		m.menu.previewInput.Focus()
	} else {
		m.menu.previewInput.Blur()
	}
}

func (m Model) handlePreviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.backMenu()
	case "ctrl+k":
		return m.closeMenu()
	case "up", "shift+tab":
		m.menu.cursor = maxInt(0, m.menu.cursor-1)
		m.focusPreviewAddress()
		return m, nil
	case "down", "tab":
		m.menu.cursor = minInt(2, m.menu.cursor+1)
		m.focusPreviewAddress()
		return m, nil
	case "enter":
		if m.menu.cursor == 0 {
			m.menu.cursor = 1
			m.focusPreviewAddress()
			return m, nil
		}
		return m.selectPreviewAction()
	case "ctrl+v":
		if m.menu.cursor == 0 {
			return m, previewPasteCmd(m.menu.previewID, clipboard.ReadAll)
		}
	}
	if m.menu.cursor != 0 {
		return m, nil
	}
	before := m.menu.previewInput.Value()
	var cmd tea.Cmd
	m.menu.previewInput, cmd = m.menu.previewInput.Update(msg)
	if m.menu.previewInput.Value() != before {
		m.previewURL = m.menu.previewInput.Value()
		m.menu.formErr = ""
	}
	return m, cmd
}

func previewPasteCmd(id uint64, read func() (string, error)) tea.Cmd {
	return func() tea.Msg {
		text, err := read()
		return previewPasteMsg{id: id, text: text, err: err}
	}
}

func (m Model) applyPreviewPaste(msg previewPasteMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeMenu || m.menu.kind != menuPreview || m.menu.previewID != msg.id {
		return m, nil
	}
	if msg.err != nil {
		m.menu.formErr = msg.err.Error()
		return m, nil
	}
	// Accept a pending clipboard reply even if the user has already moved to
	// an action row. It still belongs to the same address field and menu.
	m.menu.previewInput.Focus()
	m.menu.previewInput, _ = m.menu.previewInput.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(msg.text), Paste: true})
	m.focusPreviewAddress()
	m.previewURL = m.menu.previewInput.Value()
	m.menu.formErr = ""
	return m, nil
}

func (m Model) selectPreviewAction() (tea.Model, tea.Cmd) {
	normalized, err := browser.NormalizeURL(m.menu.previewInput.Value())
	if err != nil {
		m.menu.formErr = m.tr("preview.invalid")
		m.menu.cursor = 0
		m.focusPreviewAddress()
		return m, nil
	}
	m.menu.formErr = ""
	m.menu.previewInput.SetValue(normalized)
	m.previewURL = normalized
	return m, sitePreviewCmd(m.menu.previewID, normalized, m.menu.cursor == 2, m.language, m.dataDir, defaultPreviewLaunchers())
}

func (m Model) applyPreviewOpened(msg previewOpenedMsg) (tea.Model, tea.Cmd) {
	notice := m.tr("preview.title") + " · " + msg.url
	if msg.systemBrowser {
		notice = m.tr("preview.opened")
	}
	if msg.err != nil {
		notice = m.tr("preview.failed") + " " + msg.err.Error()
		if msg.unavailable {
			notice = m.tr("preview.unavailable")
		}
		if m.mode == modeMenu && m.menu.kind == menuPreview && m.menu.previewID == msg.id {
			m.menu.formErr = notice
			return m, nil
		}
	}
	m.setStatus(notice, msg.err == nil)
	return m, m.statusClearCmd()
}

func (m Model) renderPreviewMenu() string {
	width := maxInt(1, m.menuWidth())
	input := m.menu.previewInput
	input.Width = maxInt(1, width-4)
	input.SetCursor(input.Position())
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("preview.title")) + "\n")
	b.WriteString(m.palette.Dim.Render(truncateVisible(m.tr("preview.description"), width)) + "\n\n")
	b.WriteString(m.palette.StatusKey.Render(m.tr("preview.address")) + "\n")
	prefix := "  "
	if m.menu.cursor == 0 {
		prefix = "› "
	}
	b.WriteString(prefix + input.View() + "\n\n")
	for i, key := range []string{"preview.open", "preview.browser"} {
		row := "  " + m.tr(key)
		style := m.palette.StatusValue
		if m.menu.cursor == i+1 {
			row = "› " + m.tr(key)
			style = m.palette.HeaderMode
		}
		b.WriteString(style.Render(truncateVisible(row, width)) + "\n")
	}
	b.WriteString("\n" + m.palette.Dim.Render(truncateVisible(m.tr("preview.hint"), width)))
	if m.menu.formErr != "" {
		b.WriteString("\n\n" + m.palette.Error.Render(truncateVisible(m.menu.formErr, width)))
	}
	return b.String()
}

// The launchers keep operating-system calls inside tea.Cmd and let tests
// exercise the actual argv construction without opening windows or browsers.
type previewLaunchers struct {
	executable  func() (string, error)
	stat        func(string) (os.FileInfo, error)
	start       func(*exec.Cmd) error
	openBrowser func(string) error
}

func defaultPreviewLaunchers() previewLaunchers {
	return previewLaunchers{executable: os.Executable, stat: os.Stat, start: func(cmd *exec.Cmd) error {
		return startPreviewProcess(cmd)
	}, openBrowser: browser.Open}
}

func previewBinaryPath(executable, goos string) string {
	name := "supercli-web"
	if goos == "windows" {
		name += ".exe"
	}
	return filepath.Join(filepath.Dir(executable), name)
}

func sitePreviewCmd(id uint64, url string, systemBrowser bool, language, dataDir string, launch previewLaunchers) tea.Cmd {
	return func() tea.Msg {
		result := previewOpenedMsg{id: id, url: url, systemBrowser: systemBrowser}
		if systemBrowser {
			result.err = launch.openBrowser(url)
			return result
		}
		executable, err := launch.executable()
		if err != nil {
			result.err = err
			return result
		}
		binary := previewBinaryPath(executable, runtime.GOOS)
		info, err := launch.stat(binary)
		if err != nil || !info.Mode().IsRegular() {
			result.unavailable = true
			result.err = err
			if result.err == nil {
				result.err = errors.New("preview executable is not a regular file")
			}
			return result
		}
		cmd := childproc.NoConsoleWindow(exec.Command(binary, "--preview", url, "--preview-language", language, "--data-dir", dataDir))
		// Keep relative application resources beside the GUI binary.
		cmd.Dir = filepath.Dir(binary)
		result.err = launch.start(cmd)
		return result
	}
}

type previewProcess interface {
	Start() error
	Wait() error
}

func startPreviewProcess(process previewProcess) error {
	if err := process.Start(); err != nil {
		return err
	}
	// Wait reaps the helper when its window closes, without holding Bubble Tea
	// or its command worker open for the lifetime of the preview.
	go func() { _ = process.Wait() }()
	return nil
}
