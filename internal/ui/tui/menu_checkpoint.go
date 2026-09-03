package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) openCheckpointMenu(redo bool) (tea.Model, tea.Cmd) {
	if m.checkpointPreview == nil {
		return m.dispatchVisualCommand(map[bool]string{false: "undo", true: "redo"}[redo], "")
	}
	preview, err := m.checkpointPreview(redo)
	if err != nil {
		m.statusOverride = err.Error()
		return m.closeMenu()
	}
	preview.Redo = redo
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuCheckpoint, checkpoint: &preview}
	m.input.Blur()
	return m, nil
}

func (m Model) renderCheckpointMenu() string {
	preview := m.menu.checkpoint
	if preview == nil {
		return m.palette.Error.Render(m.tr("No checkpoint data", "Brak danych checkpointu"))
	}
	width := maxInt(24, m.menuWidth())
	action := m.tr("Undo last turn", "Cofnij ostatnią turę")
	verb := m.tr("restored to the state before the turn", "przywrócone do stanu sprzed tury")
	if preview.Redo {
		action = m.tr("Redo reverted turn", "Ponów cofniętą turę")
		verb = m.tr("restored to the state after the turn", "przywrócone do stanu po turze")
	}
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(action) + "\n")
	b.WriteString(m.palette.Dim.Render(truncateText(fmt.Sprintf(m.tr("Checkpoint %s · files will be %s. The conversation is not deleted.", "Checkpoint %s · pliki zostaną %s. Rozmowa nie jest usuwana."), preview.ID, verb), width)) + "\n\n")
	if strings.TrimSpace(preview.Prompt) != "" {
		b.WriteString(m.palette.StatusKey.Render(m.tr("Turn: ", "Tura: ")) + m.palette.StatusValue.Render(truncateText(preview.Prompt, width-8)) + "\n\n")
	}
	b.WriteString(m.palette.StatusKey.Render(fmt.Sprintf(m.tr("Files (%d):", "Pliki (%d):"), len(preview.Files))) + "\n")
	limit := minInt(len(preview.Files), maxInt(3, m.height-9))
	for _, file := range preview.Files[:limit] {
		b.WriteString(m.palette.StatusValue.Render("  • "+truncateText(file, width-4)) + "\n")
	}
	if len(preview.Files) > limit {
		b.WriteString(m.palette.Dim.Render(fmt.Sprintf(m.tr("  … and %d more", "  … i %d więcej"), len(preview.Files)-limit)) + "\n")
	}
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(m.tr("Enter confirm · Esc cancel", "Enter potwierdź · Esc anuluj"), width)))
	return b.String()
}
