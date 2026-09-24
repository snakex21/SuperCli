package tui

import (
	tea "github.com/charmbracelet/bubbletea"
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
	{id: "paste-image", group: "Pliki", title: "Obraz ze schowka", desc: "Wklej zrzut ekranu lub pliki skopiowane w Eksploratorze"},
	{id: "attach", group: "Pliki", title: "Załączniki", desc: "Wybierz obrazy, dokumenty lub kod do następnej wiadomości", shortcut: "Ctrl+O"},
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
	{id: "context-limit", group: "Model", title: "Limit kontekstu", desc: "Ustaw budżet dla aktywnego dostawcy i modelu"},
	{id: "context", group: "Praca", title: "Zawartość kontekstu", desc: "Zobacz, co zajmuje miejsce w rozmowie"},
	{id: "compact", group: "Praca", title: "Skompaktuj kontekst", desc: "Streść starszy kontekst wywołaniem modelu; zachowaj zapis rozmowy"},
	{id: "memory", group: "Praca", title: "Pamięć projektu", desc: "Przejrzyj zapisane informacje i podsumowania"},
	{id: "accounts", group: "Model", title: "Konta", desc: "Zarządzaj logowaniem i kontami ChatGPT"},
	{id: "export", group: "Pliki", title: "Eksportuj rozmowę", desc: "Zapisz rozmowę w pliku Markdown"},
}

var commonActionsEN = []actionRow{
	{id: "paste-image", group: "Files", title: "Image from clipboard", desc: "Paste a screenshot or files copied in Explorer"},
	{id: "attach", group: "Files", title: "Attachments", desc: "Choose images, documents or code for the next message", shortcut: "Ctrl+O"},
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
	{id: "context-limit", group: "Model", title: "Context limit", desc: "Set the active provider and model context budget"},
	{id: "context", group: "Work", title: "Inspect context", desc: "See what takes space in the conversation"},
	{id: "compact", group: "Work", title: "Compact context", desc: "Summarize older context with a model call; keep the saved conversation"},
	{id: "memory", group: "Work", title: "Project memory", desc: "Browse saved facts and summaries"},
	{id: "accounts", group: "Model", title: "Accounts", desc: "Manage ChatGPT sign-in and accounts"},
	{id: "export", group: "Files", title: "Export conversation", desc: "Save this conversation to a Markdown file"},
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
	m.enterMenu(interactiveMenu{kind: menuActions})
	return m, nil
}

func (m Model) openSessionsMenu() (tea.Model, tea.Cmd) {
	m.enterMenu(interactiveMenu{kind: menuSessions})
	if m.sessionStore == nil {
		return m, nil
	}
	rows, err := m.sessionStore.ListByCwd(m.home, 60)
	if err != nil {
		m.setStatus("sessions: "+err.Error(), false)
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

func (m Model) actionCategories() []string {
	if m.language == "pl" {
		return []string{"Wszystkie", "Praca", "Model", "Pliki", "Agent", "System"}
	}
	return []string{"All", "Work", "Model", "Files", "Agent", "System"}
}

func (m Model) handleActionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "left", "shift+tab", "right", "tab":
		delta := 1
		if msg.String() == "left" || msg.String() == "shift+tab" {
			delta = -1
		}
		n := len(m.actionCategories())
		m.menu.category = (m.menu.category + delta + n) % n
		m.menu.cursor = 0
		m.menu.filter = ""
		return m, nil
	}
	return m.handleSearchMenuKey(msg, func() int { return len(m.filteredActionRows()) }, m.selectAction)
}

func (m Model) handleSessionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.handleSearchMenuKey(msg, func() int { return len(m.filteredSessionRows()) }, m.selectSession)
}

func (m Model) openTranscriptSearchMenu() (tea.Model, tea.Cmd) {
	m.enterMenu(interactiveMenu{kind: menuTranscript})
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
	case "esc":
		return m.backMenu()
	case "ctrl+k":
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
	case "home":
		m.menu.cursor = 0
		return m, nil
	case "end":
		m.menu.cursor = maxInt(0, count()-1)
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
