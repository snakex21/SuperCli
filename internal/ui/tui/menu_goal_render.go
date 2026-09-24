package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/storage/goal"
)

type goalMenuRow struct {
	id, label, goalID string
	seq               int
}

func (m Model) goalMenuRows() []goalMenuRow {
	if m.goalSvc == nil {
		return nil
	}
	rows := []goalMenuRow{{id: "new", label: m.tr("+ New goal", "+ Nowy cel")}}
	if g := m.goalSvc.Active(); g != nil {
		rows = append(rows, goalMenuRow{id: "task", label: m.tr("+ Add step", "+ Dodaj krok")})
		for _, task := range m.goalTaskRows() {
			mark := "[ ]"
			if task.Status == goal.TaskDone {
				mark = "[x]"
			}
			rows = append(rows, goalMenuRow{id: "toggle", seq: task.Seq, label: fmt.Sprintf("%s %d. %s", mark, task.Seq, task.Title)})
		}
		rows = append(rows,
			goalMenuRow{id: "note", label: m.tr("Add note", "Dodaj notatkę")},
			goalMenuRow{id: "verify", label: m.tr("Record passed verification", "Potwierdź weryfikację")},
			goalMenuRow{id: "done", label: m.tr("Complete verified goal", "Zakończ zweryfikowany cel")},
			goalMenuRow{id: "pause", label: m.tr("Pause goal", "Wstrzymaj cel")})
	} else if goals, err := m.goalSvc.List(context.Background()); err == nil {
		for _, g := range goals {
			if g.Status == goal.StatusPaused {
				rows = append(rows, goalMenuRow{id: "resume", goalID: g.ID, label: m.tr("Resume: ", "Wznów: ") + g.Title})
			}
		}
	}
	return rows
}

func (m Model) handleGoalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.backMenu()
	case "up":
		m.menu.cursor = maxInt(0, m.menu.cursor-1)
	case "down":
		m.menu.cursor++
		m.clampMenuCursor()
	case "home":
		m.menu.cursor = 0
	case "end":
		m.menu.cursor = maxInt(0, len(m.goalMenuRows())-1)
	case "pgup":
		m.menu.cursor = maxInt(0, m.menu.cursor-8)
	case "pgdown":
		m.menu.cursor += 8
		m.clampMenuCursor()
	case "enter", " ":
		return m.selectGoalAction()
	case "a", "n":
		if m.goalSvc != nil {
			op := "new"
			if m.goalSvc.Active() != nil {
				op = "task"
			}
			return m.openGoalForm(op)
		}
	}
	return m, nil
}

func (m Model) openGoalForm(operation string) (tea.Model, tea.Cmd) {
	form := []string{""}
	if operation == "new" {
		form = []string{"", "", ""}
	}
	m.enterMenu(interactiveMenu{kind: menuGoalForm, editName: operation, form: form})
	return m, nil
}

func (m Model) selectGoalAction() (tea.Model, tea.Cmd) {
	rows := m.goalMenuRows()
	if len(rows) == 0 {
		return m, nil
	}
	row := rows[minInt(m.menu.cursor, len(rows)-1)]
	ctx := context.Background()
	var err error
	switch row.id {
	case "new", "task", "note", "verify":
		return m.openGoalForm(row.id)
	case "toggle":
		status := goal.TaskDone
		for _, task := range m.goalTaskRows() {
			if task.Seq == row.seq && task.Status == goal.TaskDone {
				status = goal.TaskPending
			}
		}
		err = m.goalSvc.SetTaskStatus(ctx, "", row.seq, status)
	case "done":
		err = m.goalSvc.SetStatus(ctx, "", goal.StatusDone)
	case "pause":
		err = m.goalSvc.SetStatus(ctx, "", goal.StatusPaused)
	case "resume":
		err = m.goalSvc.SetStatus(ctx, row.goalID, goal.StatusActive)
		if err == nil {
			_, err = m.goalSvc.Refresh(ctx)
		}
	}
	m.menu.formErr = ""
	if err != nil {
		m.menu.formErr = err.Error()
	}
	m.clampMenuCursor()
	return m, nil
}

func (m Model) submitGoalForm() (tea.Model, tea.Cmd) {
	if m.goalSvc == nil {
		return m, nil
	}
	if strings.TrimSpace(m.menu.form[0]) == "" {
		m.menu.formAt = 0
		m.menu.formErr = m.tr("Enter a title or text.", "Wpisz tytuł lub treść.")
		return m, nil
	}
	if m.menu.formAt < len(m.menu.form)-1 {
		m.menu.formAt++
		return m, nil
	}
	ctx := context.Background()
	value := strings.TrimSpace(m.menu.form[0])
	var err error
	switch m.menu.editName {
	case "new":
		_, err = m.goalSvc.Set(ctx, value, m.menu.form[1], m.menu.form[2], m.sessionID)
	case "task":
		_, err = m.goalSvc.AddTask(ctx, "", value)
	case "note":
		err = m.goalSvc.AppendNote(ctx, "", value)
	case "verify":
		err = m.goalSvc.Verify(ctx, "", true, value)
	}
	if err != nil {
		m.menu.formErr = err.Error()
		return m, nil
	}
	next, cmd := m.backMenu()
	parent := next.(Model)
	parent.menu.formErr = ""
	return parent, cmd
}

func (m Model) renderGoalMenu() string {
	title := m.tr("No active goal", "Brak aktywnego celu")
	if m.goalSvc != nil && m.goalSvc.Active() != nil {
		title = m.goalSvc.Active().Title
	}
	page := menuPage{title: m.tr("Goal", "Cel"), subtitle: title, detailTitle: title,
		detail: []string{m.tr("Track steps, notes and completed checks. All changes stay in project storage.", "Zapisuj kroki, notatki i wykonane sprawdzenia. Zmiany pozostają w danych projektu.")},
		footer: m.tr("↑↓ choose · Enter apply · A add step", "↑↓ wybierz · Enter zatwierdź · A dodaj krok"),
		empty:  m.tr("Goal storage is unavailable.", "Magazyn celów jest niedostępny.")}
	for _, row := range m.goalMenuRows() {
		page.items = append(page.items, menuListItem{label: row.label})
	}
	if m.menu.formErr != "" {
		page.detail = append([]string{m.menu.formErr, ""}, page.detail...)
	}
	return m.renderMenuPage(page)
}

func (m Model) renderGoalForm() string {
	width := m.menuWidth()
	title := m.tr("New goal", "Nowy cel")
	labels := []string{m.tr("Title", "Tytuł"), m.tr("Context (optional)", "Kontekst (opcjonalnie)"), m.tr("Definition of done (optional)", "Warunki ukończenia (opcjonalnie)")}
	hint := m.tr("Creating a goal pauses the previous active goal.", "Utworzenie celu wstrzyma poprzedni aktywny cel.")
	switch m.menu.editName {
	case "task":
		title = m.tr("Add step", "Dodaj krok")
		labels = []string{m.tr("Step title", "Tytuł kroku")}
		hint = ""
	case "note":
		title = m.tr("Add note", "Dodaj notatkę")
		labels = []string{m.tr("Note", "Notatka")}
		hint = ""
	case "verify":
		title = m.tr("Record passed verification", "Potwierdź weryfikację")
		labels = []string{m.tr("Evidence", "Dowód weryfikacji")}
		hint = m.tr("Describe the completed checks and their results.", "Opisz wykonane sprawdzenia i ich wyniki.")
	}
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(truncateVisible(title, width)) + "\n")
	b.WriteString(m.palette.Dim.Render(truncateVisible(hint, width)) + "\n")
	for i, label := range labels {
		prefix := "  "
		if i == m.menu.formAt {
			prefix = "> "
		}
		line := prefix + label + ": " + m.menu.form[i]
		if i == m.menu.formAt {
			line += "_"
		}
		b.WriteString(truncateVisible(line, width) + "\n")
	}
	b.WriteString(m.palette.Error.Render(truncateVisible(m.menu.formErr, width)) + "\n")
	b.WriteString(m.palette.InputHint.Render(truncateVisible(m.tr("↑↓ fields · Enter next/save · Esc cancel", "↑↓ pola · Enter dalej/zapisz · Esc anuluj"), width)))
	return b.String()
}
