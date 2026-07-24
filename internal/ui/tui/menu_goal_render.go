package tui

import (
	"fmt"
	"strings"

	"supercli/internal/storage/goal"
)

func (m Model) renderGoalMenu() string {
	rows := m.goalTaskRows()
	width := m.menuWidth()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("Goal tasks", "Zadania celu")) + "\n\n")
	start, end := 0, len(rows)
	if m.height > 0 {
		start, end = menuWindow(len(rows), m.menu.cursor, m.height-5)
	}
	for i := start; i < end; i++ {
		t := rows[i]
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "> "
		}
		mark := "[ ]"
		if t.Status == goal.TaskDone {
			mark = "[x]"
		}
		b.WriteString(truncateText(fmt.Sprintf("%s%s %d. %s", prefix, mark, t.Seq, t.Title), width) + "\n")
	}
	if len(rows) == 0 {
		b.WriteString("  " + m.tr("no active goal tasks", "brak aktywnych zadań celu") + "\n")
	}
	b.WriteString("\n" + m.palette.InputHint.Render(truncateText(m.tr("Space toggle · A add task · D delete · Esc back", "Space przełącz · A dodaj zadanie · D usuń · Esc wróć"), width)))
	return b.String()
}
