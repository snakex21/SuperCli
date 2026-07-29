package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) openQueueMenu() (tea.Model, tea.Cmd) {
	m.mode = modeMenu
	m.input.Blur()
	m.menu = interactiveMenu{kind: menuQueue}
	if m.sessionStore == nil {
		return m, nil
	}
	rows, err := m.sessionStore.ListQueuedTasks(context.Background(), m.home)
	if err != nil {
		m.statusOverride = "queue: " + err.Error()
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
		m.statusOverride = "queue: " + err.Error()
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
					m.statusOverride = m.tr("queue: enter a valid position", "kolejka: wpisz prawid\u0142ow\u0105 pozycj\u0119")
					return m, nil
				}
				if err := m.sessionStore.MoveQueuedTask(context.Background(), m.home, m.menu.moveTaskID, position-1); err != nil {
					m.statusOverride = "queue: " + err.Error()
					return m, nil
				}
				m.menu.cursor = position - 1
			} else if m.menu.editTaskID != "" {
				if err := m.sessionStore.UpdateQueuedTask(context.Background(), m.home, m.menu.editTaskID, value); err != nil {
					m.statusOverride = "queue: " + err.Error()
					return m, nil
				}
			} else {
				if _, err := m.sessionStore.EnqueueTask(context.Background(), m.home, m.sessionID, value); err != nil {
					m.statusOverride = "queue: " + err.Error()
					return m, nil
				}
			}
			m.menu.editing = false
			m.menu.editBuf = ""
			m.menu.editTaskID = ""
			m.menu.moveTaskID = ""
			m.statusOverride = ""
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
		return m.closeMenu()
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
			m.statusOverride = "queue: " + err.Error()
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
			m.statusOverride = "queue: " + err.Error()
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
	row := m.menu.tasks[minInt(m.menu.cursor, len(m.menu.tasks)-1)]
	if err := m.sessionStore.DeleteQueuedTask(context.Background(), m.home, row.ID); err != nil {
		m.statusOverride = "queue: " + err.Error()
		return m, nil
	}
	m.mode = modeNormal
	m.menu = interactiveMenu{}
	m.input.Focus()
	return m.startPrompt(row.Prompt)
}
func (m Model) renderQueueMenu() string {
	width := m.menuWidth()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("Task queue", "Kolejka zada\u0144")) + "\n")
	b.WriteString(m.palette.Dim.Render(truncateVisible(m.tr("Saved in this project and preserved after restart.", "Zapisana w tym projekcie i zachowana po ponownym uruchomieniu."), width)) + "\n\n")
	if m.menu.editing {
		label := m.tr("New task", "Nowe zadanie")
		if m.menu.editTaskID != "" {
			label = m.tr("Edit queued task", "Edytuj zadanie w kolejce")
		} else if m.menu.moveTaskID != "" {
			label = m.tr("Move queued task (1-"+strconv.Itoa(len(m.menu.tasks))+")", "Przenie\u015b zadanie (1-"+strconv.Itoa(len(m.menu.tasks))+")")
		}
		b.WriteString(m.palette.StatusKey.Render(label) + "\n")
		b.WriteString(m.palette.InputText.Render(truncateVisible("> "+m.menu.editBuf, width)) + "\n\n")
		b.WriteString(m.palette.InputHint.Render(m.tr("Enter save \u00b7 Esc cancel", "Enter zapisz \u00b7 Esc anuluj")))
		return b.String()
	}
	if m.sessionStore == nil {
		b.WriteString(m.palette.Dim.Render(m.tr("Queue storage is unavailable.", "Magazyn kolejki jest niedost\u0119pny.")) + "\n")
	} else if len(m.menu.tasks) == 0 {
		b.WriteString(m.palette.Dim.Render(m.tr("No queued tasks. Press N to add one.", "Brak zada\u0144. N dodaje pierwsze.")) + "\n")
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
	hint := m.tr("N add \u00b7 E edit \u00b7 P position \u00b7 Enter run \u00b7 Del remove \u00b7 Ctrl+\u2191\u2193 reorder \u00b7 Esc back", "N dodaj \u00b7 E edytuj \u00b7 P pozycja \u00b7 Enter uruchom \u00b7 Del usu\u0144 \u00b7 Ctrl+\u2191\u2193 przesu\u0144 \u00b7 Esc wr\u00f3\u0107")
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(hint, width)))
	return b.String()
}

func (m Model) openDataMenu() (tea.Model, tea.Cmd) {
	m.mode = modeMenu
	m.input.Blur()
	m.menu = interactiveMenu{kind: menuData}
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
			m.statusOverride = m.tr("validating backup...", "sprawdzanie kopii...")
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
		return m.closeMenu()
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
			m.statusOverride = m.tr("import is unavailable", "import jest niedostępny")
			return m, nil
		}
		m.menu.editing = true
		m.menu.editBuf = ""
		return m, nil
	}
	if m.dataExport == nil {
		m.statusOverride = m.tr("backup is unavailable", "tworzenie kopii jest niedostępne")
		return m, nil
	}
	full := m.menu.cursor == 1
	m.statusOverride = m.tr("creating backup...", "tworzenie kopii...")
	fn := m.dataExport
	return m, func() tea.Msg {
		path, err := fn(context.Background(), full)
		return dataOperationMsg{kind: "export", path: path, full: full, err: err}
	}
}

func (m Model) renderDataMenu() string {
	width := m.menuWidth()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("Backup and import", "Kopie i import")) + "\n")
	b.WriteString(m.palette.Dim.Render(truncateVisible(m.tr("Operations run locally and never call a model.", "Operacje działają lokalnie i nie wywołują modelu."), width)) + "\n\n")
	if m.menu.editing {
		b.WriteString(m.palette.StatusKey.Render(m.tr("Backup ZIP path", "Ścieżka do kopii ZIP")) + "\n")
		b.WriteString(m.palette.InputText.Render(truncateVisible("> "+m.menu.editBuf, width)) + "\n\n")
		b.WriteString(m.palette.InputHint.Render(m.tr("Enter validate and stage · Esc cancel", "Enter sprawdź i przygotuj · Esc anuluj")))
		return b.String()
	}
	rowsPL := [][2]string{
		{"Bezpieczna kopia", "sesje, pamięć, cele i ustawienia interfejsu"},
		{"Pełna kopia portable", "także klucze, modele, MCP i umiejętności"},
		{"Importuj kopię", "sprawdź ZIP; dane zostaną podmienione przy restarcie"},
	}
	rowsEN := [][2]string{
		{"Safe backup", "sessions, memory, goals and interface settings"},
		{"Full portable backup", "also keys, models, MCP and skills"},
		{"Import backup", "validate ZIP; data is replaced after restart"},
	}
	rows := rowsEN
	if m.language == "pl" {
		rows = rowsPL
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
	b.WriteString("\n" + m.palette.InputHint.Render(m.tr("↑↓ select · Enter start · Esc back", "↑↓ wybierz · Enter rozpocznij · Esc wróć")))
	return b.String()
}
