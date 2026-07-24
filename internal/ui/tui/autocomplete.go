package tui

// autocompleteKind identifies the type of popup autocomplete.
type autocompleteKind int

const (
	autocompNone    autocompleteKind = iota
	autocompSlash                    // /command
	autocompMention                  // @file
)

// autocompleteItem is one entry in the popup list.
type autocompleteItem struct {
	Label    string // display name (command name or file path)
	Desc     string // description (for commands) or file size/type (for files)
	Value    string // value inserted on accept
	Hint     string // argument hint or additional detail
	Category string // command/file category for palette grouping
}

// autocomplete is the state for the popup autocomplete.
type autocomplete struct {
	kind   autocompleteKind
	items  []autocompleteItem // full list
	cursor int                // navigation cursor
	scroll int                // scroll offset (for >8 items)
	query  string             // filter text after trigger
}

const autocompMaxVisible = 8

// triggerChar returns the character that opens this popup.
func (a autocomplete) triggerChar() string {
	switch a.kind {
	case autocompSlash:
		return "/"
	case autocompMention:
		return "@"
	default:
		return ""
	}
}

// --- Slash command autocomplete ---

// buildSlashItems builds autocomplete items from ALL known slash commands.
// We show every command from HelpContentEntries — many are handled inline
// in dispatchSlashCommand (models, providers, goal, plan, export, quit)
// and are NOT in the m.commands map, so we must not filter by it.
func buildSlashItems(_ map[string]SlashHandler, languages ...string) []autocompleteItem {
	language := "en"
	if len(languages) > 0 {
		language = normalizeLanguage(languages[0])
	}
	entries := HelpContentEntries()
	items := make([]autocompleteItem, 0, len(entries))
	for _, e := range entries {
		category := commandCategory(e.Name)
		desc := e.Desc
		if language == "pl" {
			category = polishCommandCategory(category)
			desc = polishCommandDescription(e.Name, desc)
		}
		items = append(items, autocompleteItem{
			Label:    "/" + e.Name,
			Desc:     desc,
			Hint:     e.Args,
			Category: category,
			Value:    "/" + e.Name + " ",
		})
	}
	return items
}

func polishCommandCategory(category string) string {
	return map[string]string{"system": "system", "model": "model", "agent": "agent", "session": "sesja", "account": "konto", "command": "polecenie"}[category]
}

func polishCommandDescription(name, fallback string) string {
	if desc, ok := map[string]string{
		"help": "pokaż pomoc", "goal": "zarządzaj aktywnym celem", "darwin": "uruchom N agentów i wybierz najlepszy wynik",
		"council": "zapytaj wybrane modele równolegle", "clear": "ukryj ostatnie wiadomości przed modelem", "reflect": "pokaż wzorce z refleksji",
		"compact": "skompresuj kontekst, aby oszczędzić tokeny", "status": "pokaż stan sesji i kredytów", "workers": "pokaż lub zatrzymaj workery",
		"context": "pokaż wykorzystanie kontekstu i tokenów", "context-limit": "ustaw budżet kontekstu dla aktywnego dostawcy i modelu", "mcp": "pokaż lub uruchom ponownie serwery MCP", "memory": "przeglądaj trwałą pamięć",
		"providers": "zarządzaj dostawcami", "sandbox": "pokaż stan piaskownicy", "allow-all": "zezwól na dostęp do całego systemu plików",
		"plan": "przełącz tryb planowania", "diff": "pokaż zmiany plików z sesji", "model": "wybierz spośród włączonych modeli",
		"models": "zarządzaj pełnym katalogiem modeli", "reasoning": "ustaw poziom myślenia modelu", "resume": "wznów wcześniejszą sesję",
		"export": "eksportuj sesję do Markdown", "cost": "pokaż koszty według tur", "usage": "odśwież limity subskrypcji ChatGPT",
		"undo": "cofnij ostatnią turę agenta", "redo": "przywróć cofniętą turę", "test": "uruchom testy, lint i wykrywanie wyścigów",
		"settings": "zmień ustawienia bez edycji TOML", "doctor": "sprawdź konfigurację i środowisko", "login": "zaloguj konto ChatGPT",
		"account": "pokaż bieżące konto ChatGPT", "accounts": "pokaż zalogowane konta ChatGPT", "logout": "usuń zapisane dane logowania",
		"quit": "zamknij SuperCli",
	}[name]; ok {
		return desc
	}
	return fallback
}

func commandCategory(name string) string {
	switch name {
	case "help", "status", "cost", "doctor", "sandbox", "allow-all", "context", "context-limit", "mcp", "settings":
		return "system"
	case "model", "models", "providers", "reasoning", "usage":
		return "model"
	case "goal", "plan", "darwin", "council", "reflect", "compact", "workers":
		return "agent"
	case "diff", "undo", "redo", "export", "resume", "clear", "memory":
		return "session"
	case "login", "logout", "account", "accounts":
		return "account"
	default:
		return "command"
	}
}

// HelpContentEntries returns the canonical list of slash commands for autocomplete.
// This is the same data as HelpContent() but structured for programmatic use.
func HelpContentEntries() []SlashEntry {
	return []SlashEntry{
		{Name: "help", Desc: "show this help message"},
		{Name: "goal", Desc: "manage active goal", Args: "<set|list|show|tasks|done> [args]"},
		{Name: "darwin", Desc: "run N parallel agents, pick best", Args: "[N] <prompt>"},
		{Name: "council", Desc: "ask hand-picked models in parallel (no args = pick roster)", Args: "[<prompt>]"},
		{Name: "clear", Desc: "hide recent messages from model context"},
		{Name: "reflect", Desc: "show learned patterns from reflection"},
		{Name: "compact", Desc: "compress context to save tokens"},
		{Name: "status", Desc: "show credits and session info"},
		{Name: "workers", Desc: "list coordinator workers / stop one", Args: "[stop <id>]"},
		{Name: "context", Desc: "show context/token usage breakdown"},
		{Name: "context-limit", Desc: "set working context for active provider/model", Args: "[100k|131072|1m|auto]"},
		{Name: "mcp", Desc: "list MCP servers, restart one", Args: "[restart <name>]"},
		{Name: "memory", Desc: "inspect persistent memory", Args: "[search <query> | forget <id>]"},
		{Name: "providers", Desc: "manage providers"},
		{Name: "sandbox", Desc: "show sandbox status"},
		{Name: "allow-all", Desc: "grant full filesystem access", Args: "on|off"},
		{Name: "plan", Desc: "toggle plan mode (read-only analysis)"},
		{Name: "diff", Desc: "show file changes from current session"},
		{Name: "model", Desc: "choose from enabled models", Args: "[model_id]"},
		{Name: "models", Desc: "manage the complete model catalog"},
		{Name: "reasoning", Desc: "show or set reasoning effort (OpenAI reasoning models)", Args: "[none|minimal|low|medium|high|xhigh|off]"},
		{Name: "resume", Desc: "resume a previous session", Args: "[session_id]"},
		{Name: "export", Desc: "export session to Markdown (arg 'clip' copies to clipboard)", Args: "[filename.md|clip]"},
		{Name: "cost", Desc: "show cost dashboard with per-turn breakdown"},
		{Name: "usage", Desc: "refresh and show ChatGPT-subscription usage limits (Codex)"},
		{Name: "undo", Desc: "revert the last agent turn", Args: ""},
		{Name: "redo", Desc: "restore the last reverted turn", Args: ""},
		{Name: "test", Desc: "run deterministic tests, lint and race checks", Args: "hard"},
		{Name: "settings", Desc: "edit config settings with reset-to-defaults"},
		{Name: "doctor", Desc: "diagnose SuperCli runtime and configuration"},
		{Name: "login", Desc: "sign in with ChatGPT (/login <label> adds another account)", Args: "[label]"},
		{Name: "account", Desc: "show current ChatGPT account and plan"},
		{Name: "accounts", Desc: "list logged-in ChatGPT accounts (round-robin pool)"},
		{Name: "logout", Desc: "remove saved ChatGPT credentials (/logout <label> for one)", Args: "[label]"},
		{Name: "quit", Desc: "exit SuperCli (alias: /exit)"},
	}
}

// --- @file mention autocomplete ---

// buildMentionItems scans the current directory for files and returns
// autocomplete items. Directories get a trailing slash.
