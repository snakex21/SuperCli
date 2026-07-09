package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// statusClearCmd clears a transient status override after a short delay.
func statusClearCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return statusOverrideClearMsg{} })
}

// settingKind classifies how a /settings row is edited.
type settingKind int

const (
	setTriState  settingKind = iota // tri-state *bool (default/auto → on → off → default)
	setNavigator                    // navigator string (default auto → on → off → default)
	setInt                          // integer knob (0 = built-in default)
	setText                         // free-text string knob (empty = clear/default)
	setReadonly                     // display only (changed elsewhere, e.g. /model)
	setResetAll                     // the "reset all to defaults" action row
)

// settingRow is one line in the /settings panel. Values and sources are
// computed live from the config + runtime, so the panel always reflects
// reality rather than a cached snapshot.
type settingRow struct {
	key         string // config.toml key ("" for the reset-all row)
	label       string
	desc        string
	kind        settingKind
	nextSession bool // change only takes effect on the next launch
}

// settingsRows is the ordered list of knobs the /settings panel manages.
// Every key here is cleared by "reset all"; provider entries, API keys,
// hidden models, and everything else in config.toml are left untouched.
func settingsRows() []settingRow {
	return []settingRow{
		{"orchestrator", "orchestrator", "hard delegation: main loop can only delegate via task, not edit/run itself", setTriState, true},
		{"thinking", "thinking", "chain-of-thought for local soft-switch models (Qwen /no_think)", setTriState, false},
		{"navigator", "navigator", "pre-request route (chat/advisor/coordinator) decision mode", setNavigator, true},
		{"stable_toolset", "stable_toolset", "keep the tools list fixed all session (KV-cache friendly)", setTriState, true},
		{"cache_prompt", "cache_prompt", "ask local llama.cpp servers to reuse the KV prompt cache", setTriState, true},
		{"darwin_parallel", "darwin_parallel", "run darwin best-of-N agents concurrently vs one at a time", setTriState, true},
		{"task_parallel", "task_parallel", "run multiple task delegations from one turn concurrently", setTriState, true},
		{"memory_briefing_tokens", "memory_briefing_tokens", "hard token budget for the session-start memory briefing", setInt, true},
		{"task_max_steps", "task_max_steps", "cap on model turns a delegated worker may take", setInt, true},
		{"task_max_tokens", "task_max_tokens", "cap on a delegated worker's total token spend", setInt, true},
		{"task_model", "task_model", "worker model/host for task delegation (\"model\" or \"provider/model\"; empty = coordinator's model)", setText, true},
		{"noop_gate", "noop_gate", "skip a batch (-p) run with zero LLM calls when nothing changed since the last identical run", setTriState, false},
		{"preflight_repo", "preflight_repo", "append a compact repo-state block to the first message and worker briefings (git optional)", setTriState, true},
		{"draft_verify", "draft_verify", "worker drafts file changes; objective sieve + big-model verdict gate them", setTriState, true},
		{"draft_verify_max_rounds", "draft_verify_max_rounds", "cap on REVISE round-trips before the big model takes over", setInt, true},
		{"verify_commands", "verify_commands", "objective sieve for draft-verify — semicolon-separated (e.g. go build ./... ; go test ./...)", setText, true},
		{"default_model", "default_model", "startup model — change with /model", setReadonly, false},
		{"default_provider", "default_provider", "startup provider — change with /model or /providers", setReadonly, false},
		{"", "Reset all to defaults", "remove every managed key above (providers & API keys stay untouched)", setResetAll, false},
	}
}

// settingsGlobalPath returns the global config.toml path — the same file
// /think and /orchestrator persist to.
func (m Model) settingsGlobalPath() string {
	global, _ := config.FindTomlPaths(m.dataDir, m.home)
	return global
}

// openSettingsMenu loads the global config and opens the /settings panel.
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
	rows := settingsRows()
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
	rows := settingsRows()
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
	rows := settingsRows()
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
	rows := settingsRows()
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
		case "task_max_steps":
			c.TaskMaxSteps = n
		case "task_max_tokens":
			c.TaskMaxTokens = int64(n)
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
	case "verify_commands":
		return strings.Join(c.VerifyCommands, " ; ")
	}
	return ""
}

// settingsCommitText writes the edited string knob. An empty (or
// whitespace-only) buffer clears the knob back to its default, matching
// the "reset" semantics of the other kinds.
func (m Model) settingsCommitText() (tea.Model, tea.Cmd) {
	rows := settingsRows()
	r := rows[minInt(m.menu.cursor, len(rows)-1)]
	buf := strings.TrimSpace(m.menu.editBuf)
	m.menu.editing = false
	m.menu.editBuf = ""
	return m.settingsApply(func(c *config.TomlConfig) {
		switch r.key {
		case "task_model":
			c.TaskModel = buf
		case "verify_commands":
			c.VerifyCommands = parseCommandList(buf)
		}
	})
}

// parseCommandList splits a semicolon-separated edit buffer into a
// trimmed command list, dropping empty entries. An empty input yields a
// nil slice so SaveToml omits the key entirely (= built-in default).
func parseCommandList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ";") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// cycleTri advances a tri-state *bool: nil(default/auto) → true → false → nil.
func cycleTri(p *bool) *bool {
	if p == nil {
		v := true
		return &v
	}
	if *p {
		v := false
		return &v
	}
	return nil
}

// settingToggleKey advances a switch-style knob to its next state and
// applies any live runtime side effect (thinking + cache_prompt keep an
// in-process global in sync so a same-session /model swap honours them).
func settingToggleKey(c *config.TomlConfig, key string) {
	switch key {
	case "orchestrator":
		c.Orchestrator = cycleTri(c.Orchestrator)
	case "stable_toolset":
		c.StableToolset = cycleTri(c.StableToolset)
	case "darwin_parallel":
		c.DarwinParallel = cycleTri(c.DarwinParallel)
	case "task_parallel":
		c.TaskParallel = cycleTri(c.TaskParallel)
	case "draft_verify":
		c.DraftVerify = cycleTri(c.DraftVerify)
	case "noop_gate":
		c.NoopGate = cycleTri(c.NoopGate)
	case "preflight_repo":
		c.PreflightRepo = cycleTri(c.PreflightRepo)
	case "cache_prompt":
		c.CachePrompt = cycleTri(c.CachePrompt)
		llm.SetCachePromptDefault(c.CachePrompt)
	case "thinking":
		c.Thinking = cycleTri(c.Thinking)
		llm.SetThinkingEnabled(c.Thinking == nil || *c.Thinking)
	case "navigator":
		switch c.Navigator {
		case "":
			c.Navigator = "on"
		case "on":
			c.Navigator = "off"
		default:
			c.Navigator = ""
		}
	}
}

// settingResetKey returns a knob to its default. Tri-state pointer keys
// go to nil so SaveToml drops the line entirely; scalar keys go to the
// zero sentinel that already means "use the built-in default", so a
// future default change in code is never pinned by a stale value.
func settingResetKey(c *config.TomlConfig, key string) {
	switch key {
	case "orchestrator":
		c.Orchestrator = nil
	case "stable_toolset":
		c.StableToolset = nil
	case "darwin_parallel":
		c.DarwinParallel = nil
	case "task_parallel":
		c.TaskParallel = nil
	case "cache_prompt":
		c.CachePrompt = nil
		llm.SetCachePromptDefault(nil)
	case "thinking":
		c.Thinking = nil
		llm.SetThinkingEnabled(true) // built-in default is ON
	case "navigator":
		c.Navigator = ""
	case "memory_briefing_tokens":
		c.MemoryBriefingTokens = 0
	case "task_max_steps":
		c.TaskMaxSteps = 0
	case "task_max_tokens":
		c.TaskMaxTokens = 0
	case "task_model":
		c.TaskModel = ""
	case "draft_verify":
		c.DraftVerify = nil
	case "noop_gate":
		c.NoopGate = nil
	case "preflight_repo":
		c.PreflightRepo = nil
	case "draft_verify_max_rounds":
		c.DraftVerifyMaxRounds = 0
	case "verify_commands":
		c.VerifyCommands = nil
	}
	// default_model / default_provider and the reset-all row (key == "")
	// are intentionally left untouched.
}

// settingValueSource computes a row's effective value and its source
// label (default / auto / manual / editing).
func (m Model) settingValueSource(r settingRow, c *config.TomlConfig) (value, source string) {
	switch r.key {
	case "orchestrator":
		return triDisplay(c.Orchestrator, "off")
	case "stable_toolset":
		return triDisplay(c.StableToolset, "on")
	case "thinking":
		v := "on"
		if !llm.ThinkingEnabled() {
			v = "off"
		}
		if c.Thinking == nil {
			return v, "default"
		}
		return v, "manual"
	case "cache_prompt":
		return triAutoDisplay(c.CachePrompt, "on", "off")
	case "darwin_parallel":
		return triAutoDisplay(c.DarwinParallel, "parallel", "sequential")
	case "task_parallel":
		return triAutoDisplay(c.TaskParallel, "parallel", "sequential")
	case "navigator":
		if strings.TrimSpace(c.Navigator) == "" {
			return "auto", "default"
		}
		return c.Navigator, "manual"
	case "memory_briefing_tokens":
		return intDisplay(c.MemoryBriefingTokens, "700/300 by tier")
	case "task_max_steps":
		return intDisplay(c.TaskMaxSteps, "spec or 10")
	case "task_max_tokens":
		return intDisplay(int(c.TaskMaxTokens), "no cap")
	case "task_model":
		if strings.TrimSpace(c.TaskModel) == "" {
			return "default (coordinator's model)", "default"
		}
		return c.TaskModel, "manual"
	case "draft_verify":
		return triDisplay(c.DraftVerify, "off")
	case "noop_gate":
		return triDisplay(c.NoopGate, "off")
	case "preflight_repo":
		return triDisplay(c.PreflightRepo, "off")
	case "draft_verify_max_rounds":
		return intDisplay(c.DraftVerifyMaxRounds, "2")
	case "verify_commands":
		if len(c.VerifyCommands) == 0 {
			return "none (diff-only verdict)", "default"
		}
		return strings.Join(c.VerifyCommands, " ; "), "manual"
	case "default_model":
		return dashIfEmpty(c.DefaultModel), "set via /model"
	case "default_provider":
		return dashIfEmpty(c.DefaultProvider), "set via /providers"
	}
	return "", ""
}

// triDisplay renders a tri-state with a fixed built-in default.
func triDisplay(p *bool, def string) (value, source string) {
	if p == nil {
		return def, "default"
	}
	if *p {
		return "on", "manual"
	}
	return "off", "manual"
}

// triAutoDisplay renders a tri-state whose nil means host-dependent auto.
func triAutoDisplay(p *bool, on, off string) (value, source string) {
	if p == nil {
		return "auto", "default"
	}
	if *p {
		return on, "manual"
	}
	return off, "manual"
}

func intDisplay(v int, def string) (value, source string) {
	if v == 0 {
		return "default (" + def + ")", "default"
	}
	return strconv.Itoa(v), "manual"
}

func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func (m Model) renderSettingsMenu() string {
	cfg := m.menu.settingsCfg
	if cfg == nil {
		c, _ := config.LoadToml(m.settingsGlobalPath())
		cfg = &c
	}
	rows := settingsRows()
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render("Settings") + "\n")
	b.WriteString(m.palette.InputHint.Render("value · source · (next session) = applies after restart") + "\n\n")
	for i, r := range rows {
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "❯ "
		}
		if r.kind == setResetAll {
			line := r.label
			if i == m.menu.cursor {
				line = m.palette.HeaderMode.Render(line)
			} else {
				line = m.palette.Bold.Render(line)
			}
			b.WriteString("\n" + prefix + line + "\n")
			continue
		}
		value, source := m.settingValueSource(r, cfg)
		if (r.kind == setInt || r.kind == setText) && m.menu.editing && i == m.menu.cursor {
			value = m.menu.editBuf + "_"
			source = "editing"
		}
		marker := ""
		if r.nextSession {
			marker = m.palette.Dim.Render(" (next session)")
		}
		head := fmt.Sprintf("%-24s %-22s", r.label, value)
		if i == m.menu.cursor {
			head = m.palette.HeaderMode.Render(head)
		}
		b.WriteString(prefix + head + " " + m.palette.Dim.Render(source) + marker + "\n")
	}
	sel := rows[minInt(m.menu.cursor, len(rows)-1)]
	b.WriteString("\n" + m.palette.InputHint.Render(sel.desc) + "\n")
	footer := "↑↓ move · Enter toggle/edit · [R] reset option · ESC back"
	if m.menu.editing {
		footer = "type digits · Enter save · Backspace delete · ESC cancel"
		if sel.kind == setText {
			footer = "type text · Enter save · Backspace delete · ESC cancel (empty = default)"
		}
	}
	b.WriteString("\n" + m.palette.InputHint.Render(footer))
	return b.String()
}
