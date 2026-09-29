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
		m.setStatus(err.Error(), false)
		return m.closeMenu()
	}
	preview.Redo = redo
	m.enterMenu(interactiveMenu{kind: menuCheckpoint, checkpoint: &preview})
	return m, nil
}

func (m Model) renderCheckpointMenu() string {
	preview := m.menu.checkpoint
	if preview == nil {
		return m.palette.Error.Render(m.tr("tui.menu_checkpoint.c3dd36f5c4"))
	}
	width := maxInt(24, m.menuWidth())
	action := m.tr("tui.menu_checkpoint.dd8395c0e8")
	verb := m.tr("tui.menu_checkpoint.4ec5a5babf")
	if preview.Redo {
		action = m.tr("tui.menu_checkpoint.8e0f3455fe")
		verb = m.tr("tui.menu_checkpoint.4004068fbd")
	}
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(action) + "\n")
	b.WriteString(m.palette.Dim.Render(truncateText(fmt.Sprintf(m.tr("tui.menu_checkpoint.920cafcc94"), preview.ID, verb), width)) + "\n\n")
	if strings.TrimSpace(preview.Prompt) != "" {
		b.WriteString(m.palette.StatusKey.Render(m.tr("tui.menu_checkpoint.fb5e6fc3a6")) + m.palette.StatusValue.Render(truncateText(preview.Prompt, width-8)) + "\n\n")
	}
	b.WriteString(m.palette.StatusKey.Render(fmt.Sprintf(m.tr("tui.menu_checkpoint.87986a7a53"), len(preview.Files))) + "\n")
	limit := minInt(len(preview.Files), maxInt(3, m.height-9))
	for _, file := range preview.Files[:limit] {
		b.WriteString(m.palette.StatusValue.Render("  • "+truncateText(file, width-4)) + "\n")
	}
	if len(preview.Files) > limit {
		b.WriteString(m.palette.Dim.Render(fmt.Sprintf(m.tr("tui.menu_checkpoint.bd7e794ebe"), len(preview.Files)-limit)) + "\n")
	}
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(m.tr("tui.menu_checkpoint.566b258d2d"), width)))
	return b.String()
}
