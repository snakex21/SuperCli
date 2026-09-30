package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/storage/session"
)

func (m Model) openQueueMenu() (tea.Model, tea.Cmd) {
	m.enterMenu(interactiveMenu{kind: menuQueue})
	if m.sessionStore == nil {
		return m, nil
	}
	rows, err := m.sessionStore.ListQueuedTasks(context.Background(), m.home)
	if err != nil {
		m.setStatus(m.tr("tui.menu_workflow.6d90ed4e2a")+err.Error(), false)
		return m, nil
	}
	m.menu.tasks = rows
	return m, nil
}

func (m Model) reloadQueue() Model {
	if m.sessionStore == nil {
		m.menu.tasks = nil
		return m
	}
	rows, err := m.sessionStore.ListQueuedTasks(context.Background(), m.home)
	if err != nil {
		m.setStatus(m.tr("tui.menu_workflow.6d90ed4e2a")+err.Error(), false)
		return m
	}
	m.menu.tasks = rows
	m.clampMenuCursor()
	return m
}

func (m Model) handleQueueKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.menu.editing {
		switch msg.String() {
		case "esc":
			m.menu.editing = false
			m.menu.editBuf = ""
			m.menu.editTaskID = ""
			m.menu.moveTaskID = ""
			return m, nil
		case "enter":
			value := strings.TrimSpace(m.menu.editBuf)
			if value == "" || m.sessionStore == nil {
				return m, nil
			}
			if m.menu.moveTaskID != "" {
				position, err := strconv.Atoi(value)
				if err != nil || position < 1 || position > len(m.menu.tasks) {
					m.setStatus(m.tr("tui.menu_workflow.d706a7d18b"), false)
					return m, nil
				}
				if err := m.sessionStore.MoveQueuedTask(context.Background(), m.home, m.menu.moveTaskID, position-1); err != nil {
					m.setStatus(m.tr("tui.menu_workflow.6d90ed4e2a")+err.Error(), false)
					return m, nil
				}
				m.menu.cursor = position - 1
			} else if m.menu.editTaskID != "" {
				if err := m.sessionStore.UpdateQueuedTask(context.Background(), m.home, m.menu.editTaskID, value); err != nil {
					m.setStatus(m.tr("tui.menu_workflow.6d90ed4e2a")+err.Error(), false)
					return m, nil
				}
			} else {
				if _, err := m.sessionStore.EnqueueTask(context.Background(), m.home, m.sessionID, value); err != nil {
					m.setStatus(m.tr("tui.menu_workflow.6d90ed4e2a")+err.Error(), false)
					return m, nil
				}
			}
			m.menu.editing = false
			m.menu.editBuf = ""
			m.menu.editTaskID = ""
			m.menu.moveTaskID = ""
			m.setStatus("", false)
			return m.reloadQueue(), nil
		case "backspace", "ctrl+h":
			r := []rune(m.menu.editBuf)
			if len(r) > 0 {
				m.menu.editBuf = string(r[:len(r)-1])
			}
			return m, nil
		case "ctrl+v":
			if text, err := clipboard.ReadAll(); err == nil {
				m.menu.editBuf += strings.TrimSpace(text)
			}
			return m, nil
		}
		if len(msg.Runes) > 0 {
			m.menu.editBuf += string(msg.Runes)
		}
		return m, nil
	}

	switch msg.String() {
	case "esc":
		return m.backMenu()
	case "n", "a":
		m.menu.editing = true
		m.menu.editBuf = ""
		m.menu.editTaskID = ""
		m.menu.moveTaskID = ""
		return m, nil
	case "e":
		if len(m.menu.tasks) == 0 {
			return m, nil
		}
		row := m.menu.tasks[minInt(m.menu.cursor, len(m.menu.tasks)-1)]
		m.menu.editing = true
		m.menu.editBuf = row.Prompt
		m.menu.editTaskID = row.ID
		m.menu.moveTaskID = ""
		return m, nil
	case "p":
		if len(m.menu.tasks) == 0 {
			return m, nil
		}
		row := m.menu.tasks[minInt(m.menu.cursor, len(m.menu.tasks)-1)]
		m.menu.editing = true
		m.menu.editBuf = strconv.Itoa(m.menu.cursor + 1)
		m.menu.editTaskID = ""
		m.menu.moveTaskID = row.ID
		return m, nil
	case "up":
		if m.menu.cursor > 0 {
			m.menu.cursor--
		}
		return m, nil
	case "down":
		if m.menu.cursor+1 < len(m.menu.tasks) {
			m.menu.cursor++
		}
		return m, nil
	case "ctrl+up", "ctrl+down":
		if m.sessionStore == nil || len(m.menu.tasks) == 0 {
			return m, nil
		}
		delta := -1
		if msg.String() == "ctrl+down" {
			delta = 1
		}
		to := m.menu.cursor + delta
		if to < 0 || to >= len(m.menu.tasks) {
			return m, nil
		}
		row := m.menu.tasks[m.menu.cursor]
		if err := m.sessionStore.MoveQueuedTask(context.Background(), m.home, row.ID, to); err != nil {
			m.setStatus(m.tr("tui.menu_workflow.6d90ed4e2a")+err.Error(), false)
			return m, nil
		}
		m.menu.cursor = to
		return m.reloadQueue(), nil
	case "delete", "d":
		if m.sessionStore == nil || len(m.menu.tasks) == 0 {
			return m, nil
		}
		row := m.menu.tasks[m.menu.cursor]
		if err := m.sessionStore.DeleteQueuedTask(context.Background(), m.home, row.ID); err != nil {
			m.setStatus(m.tr("tui.menu_workflow.6d90ed4e2a")+err.Error(), false)
			return m, nil
		}
		return m.reloadQueue(), nil
	case "enter":
		return m.runQueuedTask()
	}
	return m, nil
}

func (m Model) runQueuedTask() (tea.Model, tea.Cmd) {
	if m.sessionStore == nil || len(m.menu.tasks) == 0 {
		return m, nil
	}
	if m.busy {
		m.setStatus(m.tr("tui.view_markers.99032d7e36"), false)
		return m, m.statusClearCmd()
	}
	row := m.menu.tasks[minInt(m.menu.cursor, len(m.menu.tasks)-1)]
	m.mode = modeNormal
	m.menu = interactiveMenu{}
	m.input.Focus()
	if row.SessionID != "" && row.SessionID != m.sessionID {
		next, cmd := m.resumeConversation(row.SessionID)
		return next, func() tea.Msg {
			msg := cmd()
			if loaded, ok := msg.(resumeLoadedMsg); ok {
				loaded.queuedTask = &row
				return loaded
			}
			return msg
		}
	}
	return m.startQueuedPrompt(row)
}

// Queue rows already provide recovery for their prompt. A queue submission must
// leave the independent composer draft and its attachments available, and only
// remove its durable row after this specific Run has accepted the message.
func (m Model) startQueuedPrompt(row session.QueuedTask) (tea.Model, tea.Cmd) {
	drafts, selected := m.drafts, m.pendingAttachments
	m.drafts, m.pendingAttachments = nil, nil
	next, cmd := m.startPrompt(row.Prompt)
	n := next.(Model)
	n.drafts, n.pendingAttachments = drafts, selected
	n.syncInputHeight()
	if cmd == nil || n.submittingDraft == "" {
		return n, cmd
	}
	store, home := m.sessionStore, m.home
	return n, func() tea.Msg {
		msg := cmd()
		started, ok := msg.(runStartMsg)
		if !ok {
			return msg
		}
		// A rejected queue item remains in the queue, so it need not overwrite
		// an empty composer with another copy of the same prompt.
		started.draft = ""
		if started.err == nil {
			started.queueWarning = store.DeleteQueuedTask(context.Background(), home, row.ID)
		}
		return started
	}
}
func (m Model) renderQueueMenu() string {
	width := m.menuWidth()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("tui.menu_workflow.6daed5b2ef")) + "\n")
	b.WriteString(m.palette.Dim.Render(truncateVisible(m.tr("tui.menu_workflow.88b47b68bf"), width)) + "\n\n")
	if m.menu.editing {
		label := m.tr("tui.menu_workflow.3e992276b2")
		if m.menu.editTaskID != "" {
			label = m.tr("tui.menu_workflow.863e597d29")
		} else if m.menu.moveTaskID != "" {
			label = fmt.Sprintf(m.tr("tui.queue.cf3f9941f1"), len(m.menu.tasks))
		}
		b.WriteString(m.palette.StatusKey.Render(label) + "\n")
		b.WriteString(m.palette.InputText.Render(truncateVisible("> "+m.menu.editBuf, width)) + "\n\n")
		b.WriteString(m.palette.InputHint.Render(m.tr("tui.menu_context.cf88b00a0e")))
		return b.String()
	}
	if m.sessionStore == nil {
		b.WriteString(m.palette.Dim.Render(m.tr("tui.menu_workflow.48916ed9b1")) + "\n")
	} else if len(m.menu.tasks) == 0 {
		b.WriteString(m.palette.Dim.Render(m.tr("tui.menu_workflow.6f60e55100")) + "\n")
	} else {
		start, end := menuWindow(len(m.menu.tasks), m.menu.cursor, m.height-7)
		for i := start; i < end; i++ {
			row := m.menu.tasks[i]
			prefix := "  "
			if i == m.menu.cursor {
				prefix = "> "
			}
			line := fmt.Sprintf("%s%02d  %s", prefix, i+1, truncateText(strings.ReplaceAll(row.Prompt, "\n", " "), maxInt(12, width-8)))
			if i == m.menu.cursor {
				line = m.palette.HeaderMode.Render(line)
			} else {
				line = m.palette.StatusValue.Render(line)
			}
			b.WriteString(truncateVisible(line, width) + "\n")
		}
	}
	hint := m.tr("tui.menu_workflow.6d0fa5913c")
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(hint, width)))
	return b.String()
}

func (m Model) openDataMenu() (tea.Model, tea.Cmd) {
	m.enterMenu(interactiveMenu{kind: menuData})
	return m, nil
}

func (m Model) handleDataKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.menu.editing {
		switch msg.String() {
		case "esc":
			m.menu.editing = false
			m.menu.editBuf = ""
			return m, nil
		case "enter":
			path := strings.Trim(strings.TrimSpace(m.menu.editBuf), "\"")
			if path == "" || m.dataImport == nil {
				return m, nil
			}
			m.setStatus(m.tr("tui.menu_workflow.dff6f01e1e"), false)
			fn := m.dataImport
			return m, func() tea.Msg {
				full, err := fn(context.Background(), path)
				return dataOperationMsg{kind: "import", path: path, full: full, err: err}
			}
		case "backspace", "ctrl+h":
			r := []rune(m.menu.editBuf)
			if len(r) > 0 {
				m.menu.editBuf = string(r[:len(r)-1])
			}
			return m, nil
		case "ctrl+v":
			if text, err := clipboard.ReadAll(); err == nil {
				m.menu.editBuf += strings.TrimSpace(text)
			}
			return m, nil
		}
		if len(msg.Runes) > 0 {
			m.menu.editBuf += string(msg.Runes)
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		return m.backMenu()
	case "up":
		if m.menu.cursor > 0 {
			m.menu.cursor--
		}
		return m, nil
	case "down":
		if m.menu.cursor < 2 {
			m.menu.cursor++
		}
		return m, nil
	case "enter":
		return m.runDataAction()
	}
	return m, nil
}

func (m Model) runDataAction() (tea.Model, tea.Cmd) {
	if m.menu.cursor == 2 {
		if m.dataImport == nil {
			m.setStatus(m.tr("tui.menu_workflow.c6be18b8a9"), false)
			return m, nil
		}
		m.menu.editing = true
		m.menu.editBuf = ""
		return m, nil
	}
	if m.dataExport == nil {
		m.setStatus(m.tr("tui.menu_workflow.23e0a3e4e3"), false)
		return m, nil
	}
	full := m.menu.cursor == 1
	m.setStatus(m.tr("tui.menu_workflow.a23f377d51"), false)
	fn := m.dataExport
	return m, func() tea.Msg {
		path, err := fn(context.Background(), full)
		return dataOperationMsg{kind: "export", path: path, full: full, err: err}
	}
}

func (m Model) renderDataMenu() string {
	width := m.menuWidth()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("tui.menu_workflow.87e699b6c8")) + "\n")
	b.WriteString(m.palette.Dim.Render(truncateVisible(m.tr("tui.menu_workflow.8c1bca317a"), width)) + "\n\n")
	if m.menu.editing {
		b.WriteString(m.palette.StatusKey.Render(m.tr("tui.menu_workflow.0f1f10ed43")) + "\n")
		b.WriteString(m.palette.InputText.Render(truncateVisible("> "+m.menu.editBuf, width)) + "\n\n")
		b.WriteString(m.palette.InputHint.Render(m.tr("tui.menu_workflow.b24333bc33")))
		return b.String()
	}
	rows := [][2]string{
		{m.tr("tui.backup.1d7baeb78a"), m.tr("tui.backup.c5e1541efb")},
		{m.tr("tui.backup.7f043ef8e1"), m.tr("tui.backup.a3cf64c58f")},
		{m.tr("tui.backup.a4c8f47c4d"), m.tr("tui.backup.0604b24628")},
	}
	for i, row := range rows {
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "> "
		}
		line := prefix + padRight(row[0], 25) + " " + truncateText(row[1], maxInt(12, width-30))
		if i == m.menu.cursor {
			line = m.palette.HeaderMode.Render(line)
		} else {
			line = m.palette.StatusValue.Render(prefix+padRight(row[0], 25)) + " " + m.palette.Dim.Render(truncateText(row[1], maxInt(12, width-30)))
		}
		b.WriteString(truncateVisible(line, width) + "\n")
	}
	b.WriteString("\n" + m.palette.InputHint.Render(m.tr("tui.menu_workflow.ac888d2402")))
	return b.String()
}
