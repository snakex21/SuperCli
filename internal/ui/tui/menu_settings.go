package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
		{"orchestrator_model", "Model orkiestratora", "Osobny model lub provider/model koordynatora; puste oznacza model główny. Z task_model tworzy konfigurację dwumodelową.", setText, true},
		{"compact_model", "Model kompakcji", "Opcjonalny model lub provider/model do podsumowania kontekstu; puste oznacza model główny.", setText, true},
		{"task_max_steps", "Limit kroków workera", "Maksymalna liczba tur jednego delegowanego workera.", setInt, true},
		{"task_max_tokens", "Limit tokenów workera", "Maksymalny łączny budżet tokenów delegowanego workera.", setInt, true},
		{"darwin_parallel", "Równoległy Darwin", "Uruchamia warianty best-of-N równolegle zamiast po kolei.", setTriState, true},
		{"draft_verify", "Draft i weryfikacja", "Worker przygotowuje zmianę, a sito i większy model ją weryfikują.", setTriState, true},
		{"draft_verify_max_rounds", "Rundy poprawek draftu", "Limit rund REVISE zanim zadanie przejmie większy model.", setInt, true},
		{"verify_commands", "Komendy weryfikacji", "Polecenia sprawdzające oddzielone średnikami, np. go build ./... ; go test ./....", setText, true},
		{"thinking", "Tryb myślenia", "Miękkie przełączanie myślenia w lokalnych modelach, np. Qwen /no_think.", setTriState, false},
		{"stable_toolset", "Stały zestaw narzędzi", "Nie zmienia listy narzędzi w sesji, co sprzyja pamięci KV.", setTriState, true},
		{"cache_prompt", "Pamięć promptu", "Prosi lokalne serwery llama.cpp o ponowne użycie cache promptu.", setTriState, true},
		{"context_policy", "Polityka kontekstu", "Najpierw lokalnie skraca stare wyniki narzędzi przy 60%, a następnie kompaktuje przy oknie minus rezerwa.", setReadonly, false},
		{"context_window", "Globalny limit awaryjny", "Opcjonalny wspólny limit; per model i dostawca ustawiaj przez /context-limit 100k lub auto.", setInt, true},
		{"prune_protect_tokens", "Chroniony ogon narzędzi", "Liczba najświeższych tokenów wyników narzędzi chronionych przed lokalnym skróceniem; 0 skaluje się z oknem.", setInt, true},
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
		{"orchestrator_model", "Orchestrator model", "Separate model or provider/model for the coordinator; empty means the main model. Pairs with task_model for a two-model setup.", setText, true},
		{"compact_model", "Compaction model", "Optional model or provider/model for context summaries; empty means the main model.", setText, true},
		{"task_max_steps", "Worker step limit", "Maximum model turns for one delegated worker.", setInt, true},
		{"task_max_tokens", "Worker token limit", "Maximum total token budget for one delegated worker.", setInt, true},
		{"darwin_parallel", "Parallel Darwin", "Runs best-of-N variants concurrently instead of sequentially.", setTriState, true},
		{"draft_verify", "Draft verification", "A worker prepares a change and a verifier or larger model checks it.", setTriState, true},
		{"draft_verify_max_rounds", "Draft revision rounds", "Maximum REVISE rounds before the larger model takes over.", setInt, true},
		{"verify_commands", "Verification commands", "Commands separated by semicolons, e.g. go build ./... ; go test ./....", setText, true},
		{"thinking", "Thinking mode", "Soft thinking switch for local models such as Qwen /no_think.", setTriState, false},
		{"stable_toolset", "Stable tool set", "Keeps the tool list fixed during a session to preserve KV cache.", setTriState, true},
		{"cache_prompt", "Prompt cache", "Asks local llama.cpp servers to reuse the prompt KV cache.", setTriState, true},
		{"context_policy", "Context policy", "Locally trims old tool results at 60%, then compacts at window minus a generation reserve.", setReadonly, false},
		{"context_window", "Global fallback limit", "Optional shared limit; use /context-limit 100k or auto for the active provider/model.", setInt, true},
		{"prune_protect_tokens", "Protected tool tail", "Newest tool-result tokens protected from trimming; 0 scales with the model window.", setInt, true},
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
	case "orchestrator", "navigator", "task_parallel", "task_model", "orchestrator_model", "task_max_steps", "task_max_tokens", "darwin_parallel", "draft_verify", "draft_verify_max_rounds", "verify_commands":
		return "Agent i workery"
	case "thinking", "stable_toolset", "cache_prompt", "context_policy", "context_window", "prune_protect_tokens", "memory_briefing_tokens", "preflight_repo", "fallback_models", "fallback_cooldown_seconds", "compact_model":
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
	case "orchestrator", "navigator", "task_parallel", "task_model", "orchestrator_model", "task_max_steps", "task_max_tokens", "darwin_parallel", "draft_verify", "draft_verify_max_rounds", "verify_commands":
		return "Agent and workers"
	case "thinking", "stable_toolset", "cache_prompt", "context_policy", "context_window", "prune_protect_tokens", "memory_briefing_tokens", "preflight_repo", "fallback_models", "fallback_cooldown_seconds", "compact_model":
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
