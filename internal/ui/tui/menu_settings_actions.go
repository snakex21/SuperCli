package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/system/config"
)

func (m Model) openSettingsMenu() (tea.Model, tea.Cmd) {
	cfg, _ := config.LoadToml(m.settingsGlobalPath())
	m.mode = modeMenu
	m.menu = interactiveMenu{kind: menuSettings, settingsCfg: &cfg}
	m.input.Blur()
	return m, nil
}

// settingsApply loads the config fresh from disk, applies mutate, and
// saves it back. Loading fresh each time keeps provider entries and API
// keys intact even if another surface changed them, and matches how
// /think and /orchestrator write — load, modify one key, save the whole
// struct (nil pointers are omitted, so a reset truly drops the key).
func (m Model) settingsApply(mutate func(*config.TomlConfig)) (tea.Model, tea.Cmd) {
	path := m.settingsGlobalPath()
	cfg, err := config.LoadToml(path)
	if err != nil {
		m.statusOverride = "settings: " + err.Error()
		return m, statusClearCmd()
	}
	mutate(&cfg)
	if err := config.SaveToml(path, cfg); err != nil {
		m.statusOverride = "settings: save: " + err.Error()
		return m, statusClearCmd()
	}
	m.menu.settingsCfg = &cfg
	return m, nil
}

// settingsEnter acts on the selected row: toggle a switch, start editing
// a number, run the reset-all action, or ignore a read-only row.
func (m Model) settingsEnter() (tea.Model, tea.Cmd) {
	rows := m.localizedSettingsRows()
	r := rows[minInt(m.menu.cursor, len(rows)-1)]
	switch r.kind {
	case setReadonly:
		return m, nil
	case setResetAll:
		return m.settingsApply(func(c *config.TomlConfig) {
			for _, rr := range rows {
				settingResetKey(c, rr.key)
			}
		})
	case setLanguage:
		language := "pl"
		if m.language == "pl" {
			language = "en"
		}
		next, cmd := m.settingsApply(func(c *config.TomlConfig) { c.Language = language })
		mm := next.(Model)
		mm.language = language
		mm.marker = NewMarker(mm.palette, language)
		mm.chat.language = language
		mm.input.Placeholder = textFor(language, "Message SuperCli · Tab opens actions", "Napisz do SuperCli · Tab otwiera działania")
		if len(mm.chat.msgs) > 0 {
			mm.refreshTranscript()
		} else {
			mm.viewport.SetContent(welcomeAtSize(Options{Language: language, LLM: mm.llm}, mm.palette, mm.width, mm.height))
		}
		return mm, cmd
	case setInt:
		m.menu.editing = true
		m.menu.editBuf = ""
		return m, nil
	case setText:
		m.menu.editing = true
		m.menu.editBuf = settingTextValue(m.menu.settingsCfg, r.key)
		return m, nil
	default: // setTriState, setNavigator
		return m.settingsApply(func(c *config.TomlConfig) { settingToggleKey(c, r.key) })
	}
}

// settingsResetCurrent clears the selected row's key back to its default
// (removes the key / sets the zero sentinel). The reset-all row resets
// everything.
func (m Model) settingsResetCurrent() (tea.Model, tea.Cmd) {
	rows := m.localizedSettingsRows()
	r := rows[minInt(m.menu.cursor, len(rows)-1)]
	if r.kind == setResetAll {
		return m.settingsApply(func(c *config.TomlConfig) {
			for _, rr := range rows {
				settingResetKey(c, rr.key)
			}
		})
	}
	if r.key == "" {
		return m, nil
	}
	return m.settingsApply(func(c *config.TomlConfig) { settingResetKey(c, r.key) })
}

// settingsEditKey handles keystrokes while a knob is being edited. Int
// rows accept digits only; text rows accept any printable rune. Both
// share esc=cancel, enter=commit, backspace=delete.
func (m Model) settingsEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.localizedSettingsRows()
	r := rows[minInt(m.menu.cursor, len(rows)-1)]
	isText := r.kind == setText
	switch msg.String() {
	case "esc":
		m.menu.editing = false
		m.menu.editBuf = ""
		return m, nil
	case "enter":
		if isText {
			return m.settingsCommitText()
		}
		return m.settingsCommitInt()
	case "backspace", "ctrl+h":
		if len(m.menu.editBuf) > 0 {
			// Trim a whole rune, not a byte, so multi-byte input
			// (e.g. a pasted em-dash) deletes cleanly.
			runes := []rune(m.menu.editBuf)
			m.menu.editBuf = string(runes[:len(runes)-1])
		}
		return m, nil
	}
	for _, ch := range msg.Runes {
		if isText {
			m.menu.editBuf += string(ch)
		} else if ch >= '0' && ch <= '9' {
			m.menu.editBuf += string(ch)
		}
	}
	return m, nil
}

// settingsCommitInt parses the edit buffer and writes the number.
func (m Model) settingsCommitInt() (tea.Model, tea.Cmd) {
	rows := m.localizedSettingsRows()
	r := rows[minInt(m.menu.cursor, len(rows)-1)]
	buf := m.menu.editBuf
	m.menu.editing = false
	m.menu.editBuf = ""
	if buf == "" {
		return m, nil
	}
	n, err := strconv.Atoi(buf)
	if err != nil || n < 0 {
		return m, nil
	}
	return m.settingsApply(func(c *config.TomlConfig) {
		switch r.key {
		case "memory_briefing_tokens":
			c.MemoryBriefingTokens = n
		case "context_window":
			c.ContextWindow = n
		case "prune_protect_tokens":
			c.PruneProtectTokens = n
		case "task_max_steps":
			c.TaskMaxSteps = n
		case "task_max_tokens":
			c.TaskMaxTokens = int64(n)
		case "fallback_cooldown_seconds":
			c.FallbackCooldownSeconds = n
		case "draft_verify_max_rounds":
			c.DraftVerifyMaxRounds = n
		}
	})
}

// settingTextValue returns the current raw editable string for a text
// knob, so entering edit mode preloads the existing value rather than a
// blank line. List knobs render as their semicolon-joined form.
func settingTextValue(c *config.TomlConfig, key string) string {
	if c == nil {
		return ""
	}
	switch key {
	case "task_model":
		return c.TaskModel
	case "compact_model":
		return c.CompactModel
	case "verify_commands":
		return strings.Join(c.VerifyCommands, " ; ")
	case "fallback_models":
		return strings.Join(c.FallbackModels, " ; ")
	}
	return ""
}

// settingsCommitText writes the edited string knob. An empty (or
// whitespace-only) buffer clears the knob back to its default, matching
// the "reset" semantics of the other kinds.
func (m Model) settingsCommitText() (tea.Model, tea.Cmd) {
	rows := m.localizedSettingsRows()
	r := rows[minInt(m.menu.cursor, len(rows)-1)]
	buf := strings.TrimSpace(m.menu.editBuf)
	m.menu.editing = false
	m.menu.editBuf = ""
	return m.settingsApply(func(c *config.TomlConfig) {
		switch r.key {
		case "task_model":
			c.TaskModel = buf
		case "compact_model":
			c.CompactModel = buf
		case "verify_commands":
			c.VerifyCommands = parseCommandList(buf)
		case "fallback_models":
			c.FallbackModels = parseCommandList(buf)
		}
	})
}

// parseCommandList splits a semicolon-separated edit buffer into a
// trimmed command list, dropping empty entries. An empty input yields a
// nil slice so SaveToml omits the key entirely (= built-in default).
