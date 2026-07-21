package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/llm"
	"supercli/internal/system/config"
	"supercli/internal/tools/sandbox"
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
	setLanguage                     // shared en/pl UI language
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
	return settingsRowsFor("pl")
}

func settingsRowsFor(language string) []settingRow {
	if normalizeLanguage(language) != "pl" {
		return englishSettingsRows()
	}
	return []settingRow{
		{"language", "Język interfejsu", "Wspólny język aplikacji i TUI; wykrywany automatycznie tylko przy pierwszym uruchomieniu.", setLanguage, false},
		{"orchestrator", "Orkiestrator", "Auto deleguje tylko gdy warto; zawsze wymusza workery; nigdy usuwa narzędzia delegacji.", setTriState, true},
		{"navigator", "Nawigator zadań", "Dobiera tryb rozmowy, porady albo koordynacji przed rozpoczęciem zadania.", setNavigator, true},
		{"task_parallel", "Równoległe delegacje", "Uruchamia niezależne zadania workerów równocześnie, gdy backend na to pozwala.", setTriState, true},
		{"task_model", "Model workerów", "Osobny model lub provider/model dla delegowanych zadań; puste oznacza model główny.", setText, true},
		{"task_max_steps", "Limit kroków workera", "Maksymalna liczba tur jednego delegowanego workera.", setInt, true},
		{"task_max_tokens", "Limit tokenów workera", "Maksymalny łączny budżet tokenów delegowanego workera.", setInt, true},
		{"darwin_parallel", "Równoległy Darwin", "Uruchamia warianty best-of-N równolegle zamiast po kolei.", setTriState, true},
		{"draft_verify", "Draft i weryfikacja", "Worker przygotowuje zmianę, a sito i większy model ją weryfikują.", setTriState, true},
		{"draft_verify_max_rounds", "Rundy poprawek draftu", "Limit rund REVISE zanim zadanie przejmie większy model.", setInt, true},
		{"verify_commands", "Komendy weryfikacji", "Polecenia sprawdzające oddzielone średnikami, np. go build ./... ; go test ./....", setText, true},
		{"thinking", "Tryb myślenia", "Miękkie przełączanie myślenia w lokalnych modelach, np. Qwen /no_think.", setTriState, false},
		{"stable_toolset", "Stały zestaw narzędzi", "Nie zmienia listy narzędzi w sesji, co sprzyja pamięci KV.", setTriState, true},
		{"cache_prompt", "Pamięć promptu", "Prosi lokalne serwery llama.cpp o ponowne użycie cache promptu.", setTriState, true},
		{"context_policy", "Polityka kontekstu", "Najpierw lokalnie skraca stare wyniki narzędzi przy 60%, a przy 80% podsumowuje starszą rozmowę.", setReadonly, false},
		{"context_window", "Globalny limit awaryjny", "Opcjonalny wspólny limit; per model i dostawca ustawiaj przez /context-limit 100k lub auto.", setInt, true},
		{"prune_protect_tokens", "Chroniony ogon narzędzi", "Liczba najświeższych tokenów wyników narzędzi chronionych przed lokalnym skróceniem; 0 = 8192.", setInt, true},
		{"memory_briefing_tokens", "Budżet pamięci startowej", "Twardy limit tokenów przypomnienia pamięci na początku sesji.", setInt, true},
		{"preflight_repo", "Kontekst repozytorium", "Dołącza krótki stan repozytorium do pierwszej wiadomości i briefów workerów.", setTriState, true},
		{"fallback_models", "Modele awaryjne", "Opcjonalna kolejność provider/model używana po awarii głównego backendu.", setText, true},
		{"fallback_cooldown_seconds", "Przerwa po awarii", "Czas pomijania backendu, który zawiódł przed pierwszą odpowiedzią.", setInt, true},
		{"allow_all", "Dostęp poza projektem", "Pozwala pracować poza aktywnym projektem; wrażliwe foldery systemowe nadal są blokowane.", setTriState, false},
		{"noop_gate", "Pomijaj puste batch", "Kończy identyczne zadanie batch bez wywołania modelu, jeśli nic się nie zmieniło.", setTriState, false},
		{"default_model", "Domyślny model", "Model startowy; zmień go przez /model.", setReadonly, false},
		{"default_provider", "Domyślny dostawca", "Dostawca startowy; zmień go przez /model albo /providers.", setReadonly, false},
		{"", "Przywróć ustawienia domyślne", "Przywraca ustawienia powyżej; dostawcy i klucze API pozostają nietknięte.", setResetAll, false},
	}
}

func englishSettingsRows() []settingRow {
	return []settingRow{
		{"language", "Interface language", "Shared language for the desktop app and TUI; auto-detected only on first launch.", setLanguage, false},
		{"orchestrator", "Orchestrator", "Auto delegates only when useful; always forces workers; never removes delegation tools.", setTriState, true},
		{"navigator", "Task navigator", "Chooses chat, advice or coordination mode before starting a task.", setNavigator, true},
		{"task_parallel", "Parallel delegation", "Runs independent worker tasks concurrently when the backend allows it.", setTriState, true},
		{"task_model", "Worker model", "Separate model or provider/model for delegated tasks; empty means the main model.", setText, true},
		{"task_max_steps", "Worker step limit", "Maximum model turns for one delegated worker.", setInt, true},
		{"task_max_tokens", "Worker token limit", "Maximum total token budget for one delegated worker.", setInt, true},
		{"darwin_parallel", "Parallel Darwin", "Runs best-of-N variants concurrently instead of sequentially.", setTriState, true},
		{"draft_verify", "Draft verification", "A worker prepares a change and a verifier or larger model checks it.", setTriState, true},
		{"draft_verify_max_rounds", "Draft revision rounds", "Maximum REVISE rounds before the larger model takes over.", setInt, true},
		{"verify_commands", "Verification commands", "Commands separated by semicolons, e.g. go build ./... ; go test ./....", setText, true},
		{"thinking", "Thinking mode", "Soft thinking switch for local models such as Qwen /no_think.", setTriState, false},
		{"stable_toolset", "Stable tool set", "Keeps the tool list fixed during a session to preserve KV cache.", setTriState, true},
		{"cache_prompt", "Prompt cache", "Asks local llama.cpp servers to reuse the prompt KV cache.", setTriState, true},
		{"context_policy", "Context policy", "Locally trims old tool results at 60%, then summarizes older chat at 80%.", setReadonly, false},
		{"context_window", "Global fallback limit", "Optional shared limit; use /context-limit 100k or auto for the active provider/model.", setInt, true},
		{"prune_protect_tokens", "Protected tool tail", "Newest tool-result tokens protected from trimming; 0 means 8192.", setInt, true},
		{"memory_briefing_tokens", "Startup memory budget", "Hard token limit for the session-start memory briefing.", setInt, true},
		{"preflight_repo", "Repository context", "Adds a short repository state to the first message and worker briefings.", setTriState, true},
		{"fallback_models", "Fallback models", "Optional provider/model order used after primary backend failure.", setText, true},
		{"fallback_cooldown_seconds", "Failure cooldown", "How long to skip a backend that failed before its first output.", setInt, true},
		{"allow_all", "Access outside project", "Allows work outside the active project; sensitive system folders stay blocked.", setTriState, false},
		{"noop_gate", "Skip empty batches", "Finishes an identical batch task without a model call when nothing changed.", setTriState, false},
		{"default_model", "Default model", "Startup model; change it through /model.", setReadonly, false},
		{"default_provider", "Default provider", "Startup provider; change it through /model or /providers.", setReadonly, false},
		{"", "Reset all to defaults", "Resets the settings above; providers and API keys remain untouched.", setResetAll, false},
	}
}

func (m Model) localizedSettingsRows() []settingRow { return settingsRowsFor(m.language) }

func settingSection(key string) string {
	switch key {
	case "orchestrator", "navigator", "task_parallel", "task_model", "task_max_steps", "task_max_tokens", "darwin_parallel", "draft_verify", "draft_verify_max_rounds", "verify_commands":
		return "Agent i workery"
	case "thinking", "stable_toolset", "cache_prompt", "context_policy", "context_window", "prune_protect_tokens", "memory_briefing_tokens", "preflight_repo", "fallback_models", "fallback_cooldown_seconds":
		return "Modele i kontekst"
	default:
		return "System"
	}
}

func (m Model) localizedSettingSection(key string) string {
	if m.language == "pl" {
		return settingSection(key)
	}
	switch key {
	case "orchestrator", "navigator", "task_parallel", "task_model", "task_max_steps", "task_max_tokens", "darwin_parallel", "draft_verify", "draft_verify_max_rounds", "verify_commands":
		return "Agent and workers"
	case "thinking", "stable_toolset", "cache_prompt", "context_policy", "context_window", "prune_protect_tokens", "memory_briefing_tokens", "preflight_repo", "fallback_models", "fallback_cooldown_seconds":
		return "Models and context"
	default:
		return "System"
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
	case "allow_all":
		c.AllowAll = !c.AllowAll
		sandbox.SetUnsandboxed(c.AllowAll)
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
	case "allow_all":
		c.AllowAll = false
		sandbox.SetUnsandboxed(false)
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
	case "context_window":
		c.ContextWindow = 0
	case "prune_protect_tokens":
		c.PruneProtectTokens = 0
	case "task_max_steps":
		c.TaskMaxSteps = 0
	case "task_max_tokens":
		c.TaskMaxTokens = 0
	case "task_model":
		c.TaskModel = ""
	case "fallback_models":
		c.FallbackModels = nil
	case "fallback_cooldown_seconds":
		c.FallbackCooldownSeconds = 0
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
	case "language":
		if c.Language == "pl" {
			return "Polski", "manual"
		}
		return "English", "manual"
	case "orchestrator":
		if c.Orchestrator == nil {
			return "auto", "default"
		}
		if *c.Orchestrator {
			return "zawsze", "manual"
		}
		return "nigdy", "manual"
	case "allow_all":
		if c.AllowAll {
			return "on", "manual"
		}
		return "off", "default"
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
	case "context_policy":
		return "prune 60% · compact 80%", "built-in"
	case "context_window":
		return intDisplay(c.ContextWindow, "auto")
	case "prune_protect_tokens":
		return intDisplay(c.PruneProtectTokens, "8192")
	case "task_max_steps":
		return intDisplay(c.TaskMaxSteps, "spec or 10")
	case "task_max_tokens":
		return intDisplay(int(c.TaskMaxTokens), "no cap")
	case "task_model":
		if strings.TrimSpace(c.TaskModel) == "" {
			return "default (coordinator's model)", "default"
		}
		return c.TaskModel, "manual"
	case "fallback_models":
		if len(c.FallbackModels) == 0 {
			return "off (no paid fallback)", "default"
		}
		return strings.Join(c.FallbackModels, " ; "), "manual"
	case "fallback_cooldown_seconds":
		return intDisplay(c.FallbackCooldownSeconds, "30")
	case "draft_verify":
		return triDisplay(c.DraftVerify, "off")
	case "noop_gate":
		return triDisplay(c.NoopGate, "off")
	case "preflight_repo":
		return triDisplay(c.PreflightRepo, "on")
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
	rows := m.localizedSettingsRows()
	width := m.menuWidth()
	sel := rows[minInt(m.menu.cursor, len(rows)-1)]
	var b strings.Builder
	b.WriteString(m.palette.PanelTitle.Render(truncateVisible(m.tr("Settings · ", "Ustawienia · ")+m.localizedSettingSection(sel.key), width)) + "\n")
	b.WriteString(m.palette.InputHint.Render(truncateVisible(m.tr("Enter changes · R restores defaults · (next session) after restart", "Enter zmienia · R przywraca domyślne · (następna sesja) po ponownym uruchomieniu"), width)) + "\n\n")
	start, end := 0, len(rows)
	if m.height > 0 {
		start, end = menuWindow(len(rows), m.menu.cursor, m.height-7)
	}
	for i := start; i < end; i++ {
		r := rows[i]
		prefix := "  "
		if i == m.menu.cursor {
			prefix = "> "
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
		value, source = m.localizeSettingDisplay(value, source)
		if (r.kind == setInt || r.kind == setText) && m.menu.editing && i == m.menu.cursor {
			value = m.menu.editBuf + "_"
			source = "editing"
		}
		marker := ""
		if r.nextSession {
			marker = m.tr(" (next session)", " (następna sesja)")
		}
		labelWidth := 28
		valueWidth := 22
		if width < 72 {
			labelWidth = maxInt(12, width/3)
			valueWidth = maxInt(10, width-labelWidth-18)
		}
		head := fmt.Sprintf("%-*s %-*s", labelWidth, truncateText(r.label, labelWidth), valueWidth, truncateText(value, valueWidth))
		line := truncateText(prefix+head+" ["+source+"] · "+r.key+marker, width)
		if i == m.menu.cursor {
			line = m.palette.HeaderMode.Render(line)
		} else {
			line = m.palette.Dim.Render(line)
		}
		b.WriteString(line + "\n")
	}
	detail := sel.desc
	if sel.key != "" {
		detail = sel.key + " · " + detail
	}
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(detail, width)) + "\n")
	footer := m.tr("↑↓ select · Enter change · R reset · Esc back", "↑↓ wybierz · Enter zmień · R resetuj · Esc wróć")
	if m.menu.editing {
		footer = m.tr("type digits · Enter save · Backspace delete · Esc cancel", "wpisz cyfry · Enter zapisz · Backspace usuń · Esc anuluj")
		if sel.kind == setText {
			footer = m.tr("type text · Enter save · Backspace delete · Esc cancel (empty = default)", "wpisz tekst · Enter zapisz · Backspace usuń · Esc anuluj (puste = domyślne)")
		}
	}
	b.WriteString("\n" + m.palette.InputHint.Render(truncateVisible(footer, width)))
	return b.String()
}

func (m Model) localizeSettingDisplay(value, source string) (string, string) {
	if m.language != "pl" {
		if value == "zawsze" {
			value = "always"
		} else if value == "nigdy" {
			value = "never"
		}
		return value, source
	}
	replacements := map[string]string{
		"English":                       "Angielski",
		"on":                            "włączone",
		"off":                           "wyłączone",
		"parallel":                      "równolegle",
		"sequential":                    "sekwencyjnie",
		"none (diff-only verdict)":      "brak (tylko ocena zmian)",
		"off (no paid fallback)":        "wyłączone (bez płatnego zapasu)",
		"default (coordinator's model)": "domyślny (model koordynatora)",
		"prune 60% · compact 80%":       "skracanie 60% · kompakcja 80%",
	}
	if translated, ok := replacements[value]; ok {
		value = translated
	}
	if strings.HasPrefix(value, "default (") {
		value = "domyślnie (" + strings.TrimPrefix(value, "default (")
	}
	switch source {
	case "default":
		source = "domyślne"
	case "manual":
		source = "własne"
	case "built-in":
		source = "wbudowane"
	case "editing":
		source = "edycja"
	case "set via /model":
		source = "ustawiane w modelach"
	case "set via /providers":
		source = "ustawiane u dostawców"
	}
	return value, source
}
