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

func (m Model) goalMenuService() *goal.Service {
	if m.menu.filter == "global" && m.globalGoalSvc != nil {
		return m.globalGoalSvc
	}
	return m.goalSvc
}

func (m Model) goalMenuRows() []goalMenuRow {
	svc := m.goalMenuService()
	if svc == nil {
		return nil
	}
	scope, other := "project", "global"
	if m.menu.filter == "global" {
		scope, other = "global", "project"
	}
	rows := []goalMenuRow{{id: "scope_" + other, label: m.tr("goal.scope." + other)}, {id: "new", label: m.tr("tui.menu_goal_render.3c3424ee34") + " · " + m.tr("goal.scope."+scope)}}
	if g := svc.Active(); g != nil {
		rows = append(rows, goalMenuRow{id: "task", label: m.tr("tui.menu_goal_render.b86897c127")})
		for _, task := range m.goalTaskRows() {
			mark := "[ ]"
			if task.Status == goal.TaskDone {
				mark = "[x]"
			}
			rows = append(rows, goalMenuRow{id: "toggle", seq: task.Seq, label: fmt.Sprintf("%s %d. %s", mark, task.Seq, task.Title)})
		}
		rows = append(rows,
			goalMenuRow{id: "note", label: m.tr("tui.menu_goal_render.63565c0485")},
			goalMenuRow{id: "verify", label: m.tr("tui.menu_goal_render.274e155e69")},
			goalMenuRow{id: "done", label: m.tr("tui.menu_goal_render.b0d19d16b6")},
			goalMenuRow{id: "pause", label: m.tr("tui.menu_goal_render.27aa9fe4bc")})
		target := "global"
		if g.ProjectKey == goal.GlobalProjectKey {
			target = "project"
		}
		rows = append(rows, goalMenuRow{id: "assign_" + target, goalID: g.ID, label: m.tr("goal.assign." + target)})
	}
	for _, g := range m.goalMenuHistory {
		if g.Status == goal.StatusPaused {
			rows = append(rows, goalMenuRow{id: "resume", goalID: g.ID, label: m.tr("tui.menu_goal_render.5ca7346671") + g.Title})
		}
	}
	for _, g := range m.goalUnassigned {
		rows = append(rows, goalMenuRow{id: "assign_project", goalID: g.ID, label: m.tr("goal.assign.project") + ": " + g.Title}, goalMenuRow{id: "assign_global", goalID: g.ID, label: m.tr("goal.assign.global") + ": " + g.Title})
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
			if m.goalMenuService().Active() != nil {
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
	m.enterMenu(interactiveMenu{kind: menuGoalForm, editName: operation, form: form, filter: m.menu.filter})
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
	case "scope_project", "scope_global":
		m.menu.filter = strings.TrimPrefix(row.id, "scope_")
		m.menu.cursor = 0
		m.refreshGoalMenuData()
		return m, nil
	case "new", "task", "note", "verify":
		return m.openGoalForm(row.id)
	case "assign_project", "assign_global":
		err = m.goalSvc.Assign(ctx, row.goalID, row.id == "assign_global")
		if err == nil {
			m.goalUnassigned, _ = m.goalSvc.Unassigned(ctx)
			if m.globalGoalSvc != nil {
				_, _ = m.globalGoalSvc.Refresh(ctx)
			}
		}
	case "toggle":
		status := goal.TaskDone
		for _, task := range m.goalTaskRows() {
			if task.Seq == row.seq && task.Status == goal.TaskDone {
				status = goal.TaskPending
			}
		}
		err = m.goalMenuService().SetTaskStatus(ctx, "", row.seq, status)
	case "done":
		err = m.goalMenuService().SetStatus(ctx, "", goal.StatusDone)
	case "pause":
		err = m.goalMenuService().SetStatus(ctx, "", goal.StatusPaused)
	case "resume":
		err = m.goalMenuService().SetStatus(ctx, row.goalID, goal.StatusActive)
		if err == nil {
			_, err = m.goalMenuService().Refresh(ctx)
		}
	}
	m.refreshGoalMenuData()
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
		m.menu.formErr = m.tr("tui.menu_goal_render.b103152138")
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
		_, err = m.goalMenuService().Set(ctx, value, m.menu.form[1], m.menu.form[2], m.sessionID)
	case "task":
		_, err = m.goalMenuService().AddTask(ctx, "", value)
	case "note":
		err = m.goalMenuService().AppendNote(ctx, "", value)
	case "verify":
		err = m.goalMenuService().Verify(ctx, "", true, value)
	}
	if err != nil {
		m.menu.formErr = err.Error()
		return m, nil
	}
	next, cmd := m.backMenu()
	parent := next.(Model)
	parent.menu.formErr = ""
	parent.refreshGoalMenuData()
	return parent, cmd
}

func (m Model) renderGoalMenu() string {
	title := m.tr("tui.menu_goal_render.3f87b02f0d")
	if svc := m.goalMenuService(); svc != nil && svc.Active() != nil {
		title = svc.Active().Title
		scope := "project"
		if svc.Active().ProjectKey == goal.GlobalProjectKey {
			scope = "global"
		}
		title += " · " + m.tr("goal.scope."+scope)
	}
	page := menuPage{title: m.tr("tui.menu_goal_render.cdbf6975e8"), subtitle: title, detailTitle: title,
		detail: []string{m.tr("tui.menu_goal_render.af476ecc74")},
		footer: m.tr("tui.menu_goal_render.e2ee2eb5af"),
		empty:  m.tr("tui.menu_goal_render.5c9828b271")}
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
	title := m.tr("tui.menu_goal_render.f326ad6eff")
	labels := []string{m.tr("tui.menu_goal_render.7e8cd2056d"), m.tr("tui.menu_goal_render.a163618396"), m.tr("tui.menu_goal_render.0878d10bc0")}
	hint := m.tr("tui.menu_goal_render.2c1eb8a708")
	scope := "project"
	if m.menu.filter == "global" {
		scope = "global"
	}
	title += " · " + m.tr("goal.scope."+scope)
	switch m.menu.editName {
	case "task":
		title = m.tr("tui.menu_goal_render.839bd5e01e")
		labels = []string{m.tr("tui.menu_goal_render.2e9af9d4e7")}
		hint = ""
	case "note":
		title = m.tr("tui.menu_goal_render.63565c0485")
		labels = []string{m.tr("tui.menu_goal_render.d8da2c49df")}
		hint = ""
	case "verify":
		title = m.tr("tui.menu_goal_render.274e155e69")
		labels = []string{m.tr("tui.menu_goal_render.03867aea70")}
		hint = m.tr("tui.menu_goal_render.c71f87054c")
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
	b.WriteString(m.palette.InputHint.Render(truncateVisible(m.tr("tui.menu_goal_render.53217ca92b"), width)))
	return b.String()
}

// Refresh only when opening the goal menu or after a user action. Arrow keys
// render the collected rows without querying SQLite again.
func (m *Model) refreshGoalMenuData() {
	m.goalMenuTasks, m.goalMenuHistory = nil, nil
	svc := m.goalMenuService()
	if svc == nil {
		return
	}
	ctx := context.Background()
	_, _ = svc.Refresh(ctx)
	if svc != m.goalSvc && m.goalSvc != nil {
		_, _ = m.goalSvc.Refresh(ctx)
	}
	if svc.Active() != nil {
		m.goalMenuTasks, _ = svc.ListTasks(ctx, "")
	}
	m.goalMenuHistory, _ = svc.List(ctx)
}
