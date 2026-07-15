package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/storage/session"
)

// actionRow is one human-facing operation in the TUI action centre. It keeps
// slash commands as an implementation detail: the user chooses an intention,
// while selectAction reuses the existing, tested command/menu path.
type actionRow struct {
	id       string
	group    string
	title    string
	desc     string
	shortcut string
}

var commonActions = []actionRow{
	{id: "transcript", group: "Praca", title: "Przeszukaj rozmow\u0119", desc: "Znajd\u017a i zwi\u0144 pojedynczy blok", shortcut: "Ctrl+F"},
	{id: "queue", group: "Praca", title: "Kolejka zada\u0144", desc: "Zapisz zadania na p\u00f3\u017aniej i ustaw ich kolejno\u015b\u0107"},
	{id: "model", group: "Model", title: "Wybierz model", desc: "Zmień aktywny model i dostawcę"},
	{id: "models", group: "Model", title: "Katalog modeli", desc: "Włączaj i wyłączaj wszystkie wykryte modele"},
	{id: "reasoning", group: "Model", title: "Poziom myślenia", desc: "Ustaw wysiłek rozumowania modelu", shortcut: "Ctrl+R"},
	{id: "providers", group: "Model", title: "Dostawcy", desc: "Dodaj, edytuj lub sprawdź połączenie"},
	{id: "sessions", group: "Praca", title: "Ostatnie sesje", desc: "Kontynuuj wcześniejszą rozmowę"},
	{id: "projects", group: "Praca", title: "Projekty", desc: "Przełącz projekt i jego pamięć", shortcut: "Ctrl+P"},
	{id: "goal", group: "Praca", title: "Cel", desc: "Zobacz i aktualizuj trwały cel"},
	{id: "diff", group: "Pliki", title: "Zmiany w plikach", desc: "Pokaż zmiany z bieżącej sesji"},
	{id: "undo", group: "Pliki", title: "Cofnij ostatnią turę", desc: "Bezpiecznie przywróć pliki sprzed zmiany"},
	{id: "redo", group: "Pliki", title: "Ponów cofniętą turę", desc: "Przywróć ostatnio cofnięte zmiany"},
	{id: "plan", group: "Agent", title: "Tryb planowania", desc: "Przełącz analizę tylko do odczytu"},
	{id: "cost", group: "System", title: "Zużycie", desc: "Tokeny, koszt i wywołania modeli"},
	{id: "settings", group: "System", title: "Ustawienia", desc: "Zmień zachowanie CLI bez edycji TOML"},
	{id: "data", group: "System", title: "Kopie i import", desc: "Eksportuj dane lub odtwórz kopię po restarcie"},
	{id: "mcp", group: "System", title: "Serwery MCP", desc: "Pokaż wbudowane i zewnętrzne narzędzia MCP"},
	{id: "doctor", group: "System", title: "Diagnostyka", desc: "Sprawdź konfigurację i połączenia"},
	{id: "workers", group: "Agent", title: "Agenci pomocniczy", desc: "Pokaż delegowane zadania i ich stan"},
	{id: "help", group: "System", title: "Pomoc i skróty", desc: "Pokaż pełną pomoc klawiatury"},
}

var commonActionsEN = []actionRow{
	{id: "transcript", group: "Work", title: "Search conversation", desc: "Find or fold an individual block", shortcut: "Ctrl+F"},
	{id: "queue", group: "Work", title: "Task queue", desc: "Save work for later and choose its order"},
	{id: "model", group: "Model", title: "Choose model", desc: "Change the active model and provider"},
	{id: "models", group: "Model", title: "Model catalog", desc: "Enable or disable every discovered model"},
	{id: "reasoning", group: "Model", title: "Reasoning effort", desc: "Set the model reasoning level", shortcut: "Ctrl+R"},
	{id: "providers", group: "Model", title: "Providers", desc: "Add, edit or test a connection"},
	{id: "sessions", group: "Work", title: "Recent sessions", desc: "Continue an earlier conversation"},
	{id: "projects", group: "Work", title: "Projects", desc: "Switch project and its memory", shortcut: "Ctrl+P"},
	{id: "goal", group: "Work", title: "Goal", desc: "View and update the durable goal"},
	{id: "diff", group: "Files", title: "File changes", desc: "Show changes from this session"},
	{id: "undo", group: "Files", title: "Undo last turn", desc: "Safely restore files from before the change"},
	{id: "redo", group: "Files", title: "Redo reverted turn", desc: "Restore the last undone file changes"},
	{id: "plan", group: "Agent", title: "Plan mode", desc: "Toggle read-only analysis"},
	{id: "cost", group: "System", title: "Usage", desc: "Tokens, cost and model calls"},
	{id: "settings", group: "System", title: "Settings", desc: "Change CLI behavior without editing TOML"},
	{id: "data", group: "System", title: "Backup and import", desc: "Export data or restore a backup after restart"},
	{id: "mcp", group: "System", title: "MCP servers", desc: "Show built-in and external MCP tools"},
	{id: "doctor", group: "System", title: "Diagnostics", desc: "Check configuration and connections"},
	{id: "workers", group: "Agent", title: "Workers", desc: "Show delegated tasks and their state"},
	{id: "help", group: "System", title: "Help and shortcuts", desc: "Show the complete keyboard help"},
}

func (m Model) actionRows() []actionRow {
	if m.language == "pl" {
		return commonActions
	}
	return commonActionsEN
}

// ActionIDs returns the discoverable intent-first actions. It is used by the
// cross-surface contract test; the TUI itself still renders the richer rows.
func ActionIDs() []string {
	out := make([]string, 0, len(commonActionsEN))
	for _, row := range commonActionsEN {
		out = append(out, row.id)
	}
	return out
}

func (m Model) openActionsMenu() (tea.Model, tea.Cmd) {
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuActions}
	m.autocomp = autocomplete{}
	m.input.Blur()
	return m, nil
}

func (m Model) openSessionsMenu() (tea.Model, tea.Cmd) {
	m.mode = modeMenu
	m.input.Blur()
	m.menu = interactiveMenu{kind: menuSessions}
	if m.sessionStore == nil {
		return m, nil
	}
	rows, err := m.sessionStore.ListByCwd(m.home, 60)
	if err != nil {
		m.statusOverride = "sessions: " + err.Error()
		return m, nil
	}
	filtered := rows[:0]
	for _, row := range rows {
		if row.ID != m.sessionID {
			filtered = append(filtered, row)
		}
	}
	m.menu.sessions = filtered
	return m, nil
}

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
			return m, nil
		case "enter":
			prompt := strings.TrimSpace(m.menu.editBuf)
			if prompt == "" || m.sessionStore == nil {
				return m, nil
			}
			if _, err := m.sessionStore.EnqueueTask(context.Background(), m.home, m.sessionID, prompt); err != nil {
				m.statusOverride = "queue: " + err.Error()
				return m, nil
			}
			m.menu.editing = false
			m.menu.editBuf = ""
			return m.reloadQueue(), nil
		case "backspace", "ctrl+h":
			r := []rune(m.menu.editBuf)
			if len(r) > 0 {
				m.menu.editBuf = string(r[:len(r)-1])
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

func (m Model) handleActionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.handleSearchMenuKey(msg, func() int { return len(m.filteredActionRows()) }, m.selectAction)
}

func (m Model) handleSessionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.handleSearchMenuKey(msg, func() int { return len(m.filteredSessionRows()) }, m.selectSession)
}

func (m Model) openTranscriptSearchMenu() (tea.Model, tea.Cmd) {
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuTranscript}
	m.autocomp = autocomplete{}
	m.input.Blur()
	return m, nil
}

func (m Model) handleTranscriptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == " " {
		rows := m.filteredTranscriptRows()
		if len(rows) > 0 {
			m.chat.toggleMessage(rows[minInt(m.menu.cursor, len(rows)-1)].MessageIndex)
		}
		return m, nil
	}
	return m.handleSearchMenuKey(msg, func() int { return len(m.filteredTranscriptRows()) }, m.selectTranscriptMatch)
}

// handleSearchMenuKey is shared by the two intent-first menus. Deliberately
// only arrow keys navigate: ordinary letters always filter, so users never
// need to learn vi keys or command names.
func (m Model) handleSearchMenuKey(msg tea.KeyMsg, count func() int, selectRow func() (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+k":
		return m.closeMenu()
	case "up":
		if m.menu.cursor > 0 {
			m.menu.cursor--
		}
		return m, nil
	case "down":
		if m.menu.cursor+1 < count() {
			m.menu.cursor++
		}
		return m, nil
	case "pgup":
		m.menu.cursor -= 8
		if m.menu.cursor < 0 {
			m.menu.cursor = 0
		}
		return m, nil
	case "pgdown":
		m.menu.cursor += 8
		if n := count(); n == 0 {
			m.menu.cursor = 0
		} else if m.menu.cursor >= n {
			m.menu.cursor = n - 1
		}
		return m, nil
	case "backspace", "ctrl+h":
		r := []rune(m.menu.filter)
		if len(r) > 0 {
			m.menu.filter = string(r[:len(r)-1])
			m.menu.cursor = 0
		}
		return m, nil
	case "enter":
		return selectRow()
	}
	if len(msg.Runes) > 0 {
		for _, r := range msg.Runes {
			if r >= ' ' && r != 0x7f {
				m.menu.filter += string(r)
			}
		}
		m.menu.cursor = 0
	}
	return m, nil
}

func (m Model) filteredActionRows() []actionRow {
	q := strings.ToLower(strings.TrimSpace(m.menu.filter))
	if q == "" {
		return m.actionRows()
	}
	all := m.actionRows()
	rows := make([]actionRow, 0, len(all))
	for _, row := range all {
		haystack := strings.ToLower(row.group + " " + row.title + " " + row.desc)
		if strings.Contains(haystack, q) {
			rows = append(rows, row)
		}
	}
	return rows
}

func (m Model) filteredSessionRows() []session.Session {
	q := strings.ToLower(strings.TrimSpace(m.menu.filter))
	if q == "" {
		return m.menu.sessions
	}
	rows := make([]session.Session, 0, len(m.menu.sessions))
	for _, row := range m.menu.sessions {
		haystack := strings.ToLower(row.Title + " " + row.Model + " " + row.Provider + " " + row.ID)
		if strings.Contains(haystack, q) {
			rows = append(rows, row)
		}
	}
	return rows
}

func (m Model) filteredTranscriptRows() []transcriptMatch {
	return m.chat.search(m.menu.filter)
}

func (m Model) selectAction() (tea.Model, tea.Cmd) {
	rows := m.filteredActionRows()
	if len(rows) == 0 {
		return m, nil
	}
	switch rows[minInt(m.menu.cursor, len(rows)-1)].id {
	case "model":
		return m.openModelsMenu()
	case "models":
		return m.openModelCatalogMenu()
	case "reasoning":
		return m.openReasoningMenu()
	case "providers":
		return m.openProvidersMenu()
	case "sessions":
		return m.openSessionsMenu()
	case "transcript":
		return m.openTranscriptSearchMenu()
	case "queue":
		return m.openQueueMenu()
	case "projects":
		return m.openProjectsMenu()
	case "goal":
		return m.openGoalMenu()
	case "settings":
		return m.openSettingsMenu()
	case "data":
		return m.openDataMenu()
	case "undo":
		return m.openCheckpointMenu(false)
	case "redo":
		return m.openCheckpointMenu(true)
	case "diff", "plan", "cost", "mcp", "doctor", "workers", "help":
		return m.dispatchVisualCommand(rows[minInt(m.menu.cursor, len(rows)-1)].id, "")
	default:
		return m, nil
	}
}

func (m Model) renderQueueMenu() string {
	width := m.menuWidth()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("Task queue", "Kolejka zada\u0144")) + "\n")
	b.WriteString(m.palette.Dim.Render(truncateVisible(m.tr("Saved in this project and preserved after restart.", "Zapisana w tym projekcie i zachowana po ponownym uruchomieniu."), width)) + "\n\n")
	if m.menu.editing {
		b.WriteString(m.palette.StatusKey.Render(m.tr("New task", "Nowe zadanie")) + "\n")
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
	hint := m.tr("N add \u00b7 Enter run \u00b7 Del remove \u00b7 Ctrl+\u2191\u2193 reorder \u00b7 Esc back", "N dodaj \u00b7 Enter uruchom \u00b7 Del usu\u0144 \u00b7 Ctrl+\u2191\u2193 przesu\u0144 \u00b7 Esc wr\u00f3\u0107")
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

func (m Model) selectSession() (tea.Model, tea.Cmd) {
	rows := m.filteredSessionRows()
	if len(rows) == 0 {
		return m, nil
	}
	return m.dispatchVisualCommand("resume", rows[minInt(m.menu.cursor, len(rows)-1)].ID)
}

func (m Model) selectTranscriptMatch() (tea.Model, tea.Cmd) {
	rows := m.filteredTranscriptRows()
	if len(rows) == 0 {
		return m, nil
	}
	selected := rows[minInt(m.menu.cursor, len(rows)-1)]
	line := m.chat.renderedLineForMessage(selected.MessageIndex, m.palette)
	m.mode = modeNormal
	m.menu = interactiveMenu{}
	m.input.Focus()
	m.refreshTranscript()
	m.viewport.SetYOffset(maxInt(0, line-1))
	return m, nil
}

func (m Model) renderTranscriptMenu() string {
	rows := m.filteredTranscriptRows()
	width := m.menuWidth()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("Search conversation", "Przeszukaj rozmow\u0119")) + "\n")
	b.WriteString(m.palette.Dim.Render(truncateVisible(m.tr("Enter jumps to a block; Space folds or unfolds multi-line blocks.", "Enter przechodzi do bloku; Spacja zwija lub rozwija bloki wielowierszowe."), width)) + "\n")
	b.WriteString(m.palette.InputHint.Render(truncateVisible(m.tr("Search: ", "Szukaj: ")+m.menu.filter, width)) + "\n\n")
	if len(rows) == 0 {
		b.WriteString(m.palette.Dim.Render(m.tr("  No matching messages.", "  Brak pasuj\u0105cych wiadomo\u015bci.")) + "\n")
	} else {
		start, end := menuWindow(len(rows), m.menu.cursor, maxInt(3, m.height-7))
		for i := start; i < end; i++ {
			row := rows[i]
			who := "System"
			switch row.Role {
			case roleUser:
				who = m.tr("You", "Ty")
			case roleAssistant:
				who = "SuperCli"
			}
			fold := " "
			if m.chat.isFoldable(row.MessageIndex) {
				fold = "\u25be"
				if m.chat.msgs[row.MessageIndex].collapsed {
					fold = "\u25b8"
				}
			}
			prefix := "  "
			if i == m.menu.cursor {
				prefix = "> "
			}
			label := fmt.Sprintf("%s%s %-9s ", prefix, fold, who)
			preview := truncateText(row.Preview, maxInt(12, width-len([]rune(label))-1))
			line := m.palette.StatusKey.Render(label) + m.palette.Dim.Render(preview)
			if i == m.menu.cursor {
				line = m.palette.HeaderMode.Render(label) + m.palette.StatusValue.Render(preview)
			}
			b.WriteString(truncateVisible(line, width) + "\n")
		}
	}
	hint := m.tr("\u2191\u2193 select \u00b7 Enter jump \u00b7 Space fold \u00b7 type to search \u00b7 Esc back", "\u2191\u2193 wybierz \u00b7 Enter przejd\u017a \u00b7 Spacja zwi\u0144 \u00b7 pisz aby szuka\u0107 \u00b7 Esc wr\u00f3\u0107")
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(hint, width)))
	return b.String()
}

func (m Model) dispatchVisualCommand(name, args string) (tea.Model, tea.Cmd) {
	next, _ := m.closeMenu()
	mm := next.(Model)
	return mm.dispatchSlashCommand(SlashCommand{Name: name, Args: args, Quiet: true})
}

func (m Model) renderActionsMenu() string {
	rows := m.filteredActionRows()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("Action centre", "Centrum działań")) + "\n")
	b.WriteString(m.palette.Dim.Render(truncateVisible(m.tr("Common functions without typing commands. Start typing to filter.", "Najczęstsze funkcje bez wpisywania komend. Zacznij pisać, aby filtrować."), m.menuWidth())) + "\n")
	b.WriteString(m.palette.InputHint.Render(truncateVisible(m.tr("Search: ", "Szukaj: ")+m.menu.filter, m.menuWidth())) + "\n\n")
	if len(rows) == 0 {
		b.WriteString(m.palette.Dim.Render(m.tr("  No matching actions.", "  Brak pasujących działań.")) + "\n")
	} else {
		compact := m.menuWidth() < 72
		rowHeight := 1
		if compact {
			rowHeight = 2
		}
		start, end := menuWindow(len(rows), m.menu.cursor, (m.height-7)/rowHeight)
		for i := start; i < end; i++ {
			row := rows[i]
			cursor := "  "
			if i == m.menu.cursor {
				cursor = "> "
			}
			if compact {
				width := maxInt(12, m.menuWidth()-4)
				title := truncateText(row.title, width)
				first := cursor + title
				if i == m.menu.cursor {
					first = m.palette.HeaderMode.Render(first)
				} else {
					first = m.palette.Bold.Render(first)
				}
				meta := "    " + strings.ToUpper(row.group) + " · " + row.desc
				b.WriteString(truncateVisible(first, m.menuWidth()) + "\n")
				b.WriteString(m.palette.Dim.Render(truncateText(meta, width)) + "\n")
				continue
			}
			group := padRight(truncateText(strings.ToUpper(row.group), 10), 10)
			title := padRight(truncateText(row.title, 25), 25)
			descWidth := m.width - 45
			if descWidth < 16 {
				descWidth = 16
			}
			line := cursor + m.palette.StatusKey.Render(group) + " " + m.palette.Bold.Render(title) + " " + m.palette.Dim.Render(truncateText(row.desc, descWidth))
			if row.shortcut != "" && m.width >= 92 {
				line += "  " + m.palette.InputHint.Render(row.shortcut)
			}
			if i == m.menu.cursor {
				line = m.palette.HeaderMode.Render(cursor+group+" "+title) + " " + m.palette.StatusValue.Render(truncateText(row.desc, descWidth))
				if row.shortcut != "" && m.width >= 92 {
					line += "  " + m.palette.InputHint.Render(row.shortcut)
				}
			}
			b.WriteString(line + "\n")
		}
	}
	hint := m.tr("↑↓ select · Enter open · type to search · Esc back · / advanced commands", "↑↓ wybierz · Enter otwórz · pisz aby szukać · Esc wróć · / komendy zaawansowane")
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(hint, m.menuWidth())))
	return b.String()
}

func (m Model) renderSessionsMenu() string {
	rows := m.filteredSessionRows()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(m.tr("Recent sessions", "Ostatnie sesje")) + "\n")
	project := filepath.Base(m.home)
	if project == "." || project == "" {
		project = m.home
	}
	b.WriteString(m.palette.Dim.Render(truncateVisible(m.tr("Project: ", "Projekt: ")+project+m.tr(" · original sessions remain unchanged", " · oryginalne sesje pozostają bez zmian"), m.menuWidth())) + "\n")
	b.WriteString(m.palette.InputHint.Render(truncateVisible(m.tr("Search: ", "Szukaj: ")+m.menu.filter, m.menuWidth())) + "\n\n")
	if m.sessionStore == nil {
		b.WriteString(m.palette.Dim.Render(m.tr("  Session history is unavailable.", "  Historia sesji nie jest dostępna.")) + "\n")
	} else if len(rows) == 0 {
		b.WriteString(m.palette.Dim.Render(m.tr("  No earlier sessions in this project.", "  Brak wcześniejszych sesji w tym projekcie.")) + "\n")
	} else {
		compact := m.menuWidth() < 86
		rowHeight := 1
		if compact {
			rowHeight = 2
		}
		start, end := menuWindow(len(rows), m.menu.cursor, (m.height-7)/rowHeight)
		for i := start; i < end; i++ {
			row := rows[i]
			title := strings.TrimSpace(row.Title)
			if title == "" {
				title = m.tr("Untitled", "Bez tytułu")
			}
			when := relativeSessionTime(row.UpdatedAt)
			model := row.Model
			if row.Provider != "" && !strings.HasPrefix(model, row.Provider+"/") {
				model = row.Provider + "/" + model
			}
			cursor := "  "
			if i == m.menu.cursor {
				cursor = "> "
			}
			if compact {
				width := maxInt(12, m.menuWidth()-4)
				first := cursor + truncateText(title, width-2)
				meta := fmt.Sprintf("    %s · %d msg · %s", when, row.MessageCount, model)
				if i == m.menu.cursor {
					first = m.palette.HeaderMode.Render(first)
				} else {
					first = m.palette.StatusValue.Render(first)
				}
				b.WriteString(truncateVisible(first, m.menuWidth()) + "\n")
				b.WriteString(m.palette.Dim.Render(truncateText(meta, width)) + "\n")
				continue
			}
			line := cursor + padRight(truncateText(title, 38), 38) + " " + padRight(truncateText(when, 15), 15) + " " + fmt.Sprintf("%3d msg", row.MessageCount) + "  " + truncateText(model, 32)
			if i == m.menu.cursor {
				line = m.palette.HeaderMode.Render(line)
			} else {
				line = m.palette.StatusValue.Render(cursor+padRight(truncateText(title, 38), 38)) + " " + m.palette.Dim.Render(padRight(truncateText(when, 15), 15)+" "+fmt.Sprintf("%3d msg", row.MessageCount)+"  "+truncateText(model, 32))
			}
			b.WriteString(line + "\n")
		}
	}
	hint := m.tr("↑↓ select · Enter continue · type to search · Esc back", "↑↓ wybierz · Enter kontynuuj · pisz aby szukać · Esc wróć")
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(hint, m.menuWidth())))
	return b.String()
}

func menuWindow(total, cursor, available int) (int, int) {
	// Before Bubble Tea delivers its first WindowSizeMsg there is no known
	// height. Render the complete compact menu instead of an arbitrary five
	// rows; the next frame will apply the real terminal height.
	if available <= 0 {
		available = 16
	} else if available < 5 {
		available = 5
	}
	if available > 16 {
		available = 16
	}
	if total <= available {
		return 0, total
	}
	start := cursor - available/2
	if start < 0 {
		start = 0
	}
	if start+available > total {
		start = total - available
	}
	return start, start + available
}

func relativeSessionTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	local := t.Local()
	now := time.Now()
	y, d := local.Year(), local.YearDay()
	switch {
	case y == now.Year() && d == now.YearDay():
		return "dzisiaj " + local.Format("15:04")
	case y == now.Year() && d == now.YearDay()-1:
		return "wczoraj " + local.Format("15:04")
	default:
		return local.Format("02.01.2006")
	}
}
