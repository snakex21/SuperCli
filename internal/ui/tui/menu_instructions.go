package tui

import (
	"fmt"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"

	"supercli/internal/llm/prompt"
)

type instructionEditor struct {
	id            string // empty for a new preset
	name, content instructionBuffer
	field         int
}

func (m Model) openInstructionsMenu() (tea.Model, tea.Cmd) {
	state, err := prompt.LoadUserInstructions(m.dataDir)
	m.enterMenu(interactiveMenu{kind: menuInstructions, instructions: state, instructionsReady: err == nil})
	if err != nil {
		m.menu.formErr = fmt.Sprintf(m.tr("tui.instructions.load_error"), err)
	}
	for i, preset := range state.Presets {
		if preset.ID == state.ActiveID {
			m.menu.cursor = i
		}
	}
	return m, nil
}

// Always save a detached candidate. A failed write must not change the visible
// active/enabled state or any preset, nor discard an editor's draft.
func (m *Model) saveInstructions(candidate prompt.UserInstructionsState) bool {
	saved, err := prompt.SaveUserInstructions(m.dataDir, candidate)
	if err != nil {
		m.menu.formErr = fmt.Sprintf(m.tr("tui.instructions.save_error"), err)
		return false
	}
	m.menu.instructions = saved
	m.menu.formErr = ""
	m.setStatus(m.tr("tui.instructions.saved"), false)
	return true
}

func (m Model) handleInstructionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.menu.kind == menuInstructionEditor {
		return m.handleInstructionEditorKey(msg)
	}
	switch msg.String() {
	case "esc":
		return m.backMenu()
	case "ctrl+k":
		return m.closeMenu()
	case "r":
		return m.openInstructionsMenu()
	}
	if !m.menu.instructionsReady {
		return m, nil
	}
	state := m.menu.instructions
	count := len(state.Presets)
	switch msg.String() {
	case "up", "k":
		m.menu.cursor = maxInt(0, m.menu.cursor-1)
	case "down", "j":
		m.menu.cursor = maxInt(0, minInt(count-1, m.menu.cursor+1))
	case "home":
		m.menu.cursor = 0
	case "end":
		m.menu.cursor = maxInt(0, count-1)
	case "n":
		m.enterMenu(interactiveMenu{kind: menuInstructionEditor, instructions: state, instructionsReady: true})
	case "e":
		if count > 0 {
			preset := state.Presets[m.menu.cursor]
			m.enterMenu(interactiveMenu{kind: menuInstructionEditor, instructions: state, instructionsReady: true,
				instructionEditor: instructionEditor{id: preset.ID, name: newInstructionBuffer(preset.Name), content: newInstructionBuffer(preset.Content)}})
		}
	case "enter":
		if count > 0 {
			state.ActiveID = state.Presets[m.menu.cursor].ID
			m.saveInstructions(state)
		}
	case " ", "t":
		if count == 0 {
			m.menu.formErr = m.tr("tui.instructions.need_preset")
			break
		}
		state.Enabled = !state.Enabled
		m.saveInstructions(state)
	}
	return m, nil
}

func (m Model) handleInstructionEditorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	editor := &m.menu.instructionEditor
	switch msg.String() {
	case "esc":
		return m.backMenu()
	case "ctrl+k":
		return m.closeMenu()
	case "tab", "shift+tab":
		editor.field = 1 - editor.field
		return m, nil
	case "ctrl+s":
		name, content := strings.TrimSpace(editor.name.value()), editor.content.value()
		if name == "" {
			m.menu.formErr = m.tr("tui.instructions.name_required")
			return m, nil
		}
		if strings.TrimSpace(content) == "" {
			m.menu.formErr = m.tr("tui.instructions.content_required")
			return m, nil
		}
		candidate := m.menu.instructions
		candidate.Presets = append([]prompt.UserInstructionPreset(nil), candidate.Presets...)
		id := editor.id
		if id == "" {
			id = uuid.NewString()
			candidate.Presets = append(candidate.Presets, prompt.UserInstructionPreset{ID: id, Name: name, Content: content})
			if candidate.ActiveID == "" {
				candidate.ActiveID = id
			}
		} else {
			for i := range candidate.Presets {
				if candidate.Presets[i].ID == id {
					candidate.Presets[i].Name, candidate.Presets[i].Content = name, content
					break
				}
			}
		}
		if !m.saveInstructions(candidate) {
			return m, nil
		}
		saved := m.menu.instructions
		// Replace the list snapshot, rather than returning to stale pre-save data.
		parent := m.menu.parent
		m.menu = interactiveMenu{kind: menuInstructions, instructions: saved, instructionsReady: true}
		if parent != nil {
			m.menu.parent = parent.parent
		}
		for i, preset := range saved.Presets {
			if preset.ID == id {
				m.menu.cursor = i
			}
		}
		return m, nil
	}
	buffer := &editor.name
	if editor.field == 1 {
		buffer = &editor.content
	}
	if msg.String() == "ctrl+v" {
		text, err := clipboard.ReadAll()
		if err != nil {
			m.menu.formErr = fmt.Sprintf(m.tr("tui.instructions.paste_error"), err)
			return m, nil
		}
		if editor.field == 0 {
			text = strings.ReplaceAll(strings.ReplaceAll(text, "\r", " "), "\n", " ")
		}
		buffer.insert(text)
	} else {
		// Keyboard handling sees terminal dimensions; rendering receives the
		// menu's inset rectangle. Use the same inner viewport for both.
		screenWidth, screenHeight := m.width, m.height
		if screenWidth <= 0 {
			screenWidth = 100
		}
		if screenHeight <= 0 {
			screenHeight = 32
		}
		width, height := instructionEditorSize(minInt(screenWidth, 120)-4, screenHeight-4)
		if editor.field == 0 {
			height = 1
		}
		buffer.update(msg, editor.field == 1, width, height)
	}
	m.menu.formErr = ""
	return m, nil
}

func (m Model) renderInstructionsMenu() string {
	state := m.menu.instructions
	status := m.tr("tui.instructions.disabled")
	if state.Enabled {
		status = m.tr("tui.instructions.enabled")
	}
	items := make([]menuListItem, 0, len(state.Presets))
	for _, preset := range state.Presets {
		badge := ""
		if preset.ID == state.ActiveID {
			badge = m.tr("tui.instructions.active")
		}
		items = append(items, menuListItem{label: preset.Name, meta: fmt.Sprintf(m.tr("tui.instructions.tokens"), prompt.EstimateInstructionTokens(preset.Content)), badge: badge})
	}
	return m.renderMenuPage(menuPage{title: m.tr("tui.instructions.title"), subtitle: status + " · " + m.tr("tui.instructions.next_message"),
		detailTitle: m.tr("tui.instructions.title"), detail: []string{m.tr("tui.instructions.next_message")}, items: items, empty: m.tr("tui.instructions.empty"), footer: status + " · " + m.tr("tui.instructions.list_keys")})
}

func instructionEditorSize(width, height int) (int, int) {
	return maxInt(8, minInt(width, 116)), maxInt(1, height-8)
}

func (m Model) renderInstructionEditor() string {
	e := m.menu.instructionEditor
	width, height := instructionEditorSize(m.width, m.height)
	lines := []string{m.palette.Header.Render(m.tr("tui.instructions.title")),
		m.palette.Dim.Render(m.tr("tui.instructions.editor_keys")),
		m.tr("tui.instructions.name"), e.name.view(width, 1, e.field == 0, m.palette),
		m.tr("tui.instructions.content") + " · " + fmt.Sprintf(m.tr("tui.instructions.tokens"), prompt.EstimateInstructionTokens(e.content.value())),
		e.content.view(width, height, e.field == 1, m.palette)}
	if m.menu.formErr != "" {
		lines = append(lines, m.palette.Error.Render(m.menu.formErr))
	}
	return strings.Join(lines, "\n")
}
