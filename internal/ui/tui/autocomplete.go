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
		category = localizedCommandCategory(language, category)
		desc := localizedCommandDescription(language, e.Name, e.Desc)
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

func localizedCommandCategory(language, category string) string {
	return textFor(language, "tui.command_category."+category)
}
func localizedCommandDescription(language, name, fallback string) string {
	value := textFor(language, "tui.command_desc."+name)
	if value == "tui.command_desc."+name {
		return fallback
	}
	return value
}
func polishCommandCategory(category string) string { return localizedCommandCategory("pl", category) }
func polishCommandDescription(name, fallback string) string {
	return localizedCommandDescription("pl", name, fallback)
}

func commandCategory(name string) string {
	switch name {
	case "help", "status", "cost", "doctor", "sandbox", "allow-all", "context", "context-limit", "mcp", "settings", "update":
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
		{Name: "help", Desc: textFor("en", "tui.command_desc.help")},
		{Name: "goal", Desc: textFor("en", "tui.command_desc.goal"), Args: "<set|list|show|tasks|done> [args]"},
		{Name: "darwin", Desc: textFor("en", "tui.command_desc.darwin"), Args: "[N] <prompt>"},
		{Name: "council", Desc: textFor("en", "tui.command_desc.council"), Args: "[<prompt>]"},
		{Name: "clear", Desc: textFor("en", "tui.command_desc.clear")},
		{Name: "reflect", Desc: textFor("en", "tui.command_desc.reflect")},
		{Name: "compact", Desc: textFor("en", "tui.command_desc.compact")},
		{Name: "status", Desc: textFor("en", "tui.command_desc.status")},
		{Name: "workers", Desc: textFor("en", "tui.command_desc.workers"), Args: "[stop <id>]"},
		{Name: "context", Desc: textFor("en", "tui.command_desc.context")},
		{Name: "context-limit", Desc: textFor("en", "tui.command_desc.context-limit"), Args: "[100k|131072|1m|auto]"},
		{Name: "mcp", Desc: textFor("en", "tui.command_desc.mcp"), Args: "[restart <name>]"},
		{Name: "memory", Desc: textFor("en", "tui.command_desc.memory"), Args: "[search <query> | forget <id>]"},
		{Name: "providers", Desc: textFor("en", "tui.command_desc.providers")},
		{Name: "sandbox", Desc: textFor("en", "tui.command_desc.sandbox")},
		{Name: "allow-all", Desc: textFor("en", "tui.command_desc.allow-all"), Args: "on|off"},
		{Name: "plan", Desc: textFor("en", "tui.command_desc.plan")},
		{Name: "diff", Desc: textFor("en", "tui.command_desc.diff")},
		{Name: "model", Desc: textFor("en", "tui.command_desc.model"), Args: "[model_id]"},
		{Name: "models", Desc: textFor("en", "tui.command_desc.models")},
		{Name: "reasoning", Desc: textFor("en", "tui.command_desc.reasoning"), Args: "[none|minimal|low|medium|high|xhigh|max|off]"},
		{Name: "resume", Desc: textFor("en", "tui.command_desc.resume"), Args: "[session_id]"},
		{Name: "export", Desc: textFor("en", "tui.command_desc.export"), Args: "[filename.md|clip]"},
		{Name: "cost", Desc: textFor("en", "tui.command_desc.cost")},
		{Name: "usage", Desc: textFor("en", "tui.command_desc.usage")},
		{Name: "undo", Desc: textFor("en", "tui.command_desc.undo"), Args: ""},
		{Name: "redo", Desc: textFor("en", "tui.command_desc.redo"), Args: ""},
		{Name: "test", Desc: textFor("en", "tui.command_desc.test"), Args: "hard"},
		{Name: "settings", Desc: textFor("en", "tui.command_desc.settings")},
		{Name: "doctor", Desc: textFor("en", "tui.command_desc.doctor")},
		{Name: "login", Desc: textFor("en", "tui.command_desc.login"), Args: "[label]"},
		{Name: "account", Desc: textFor("en", "tui.command_desc.account")},
		{Name: "accounts", Desc: textFor("en", "tui.command_desc.accounts")},
		{Name: "logout", Desc: textFor("en", "tui.command_desc.logout"), Args: "[label]"},
		{Name: "update", Desc: textFor("en", "tui.command_desc.update"), Args: "[check|download|install]"},
		{Name: "quit", Desc: textFor("en", "tui.command_desc.quit")},
	}
}

// --- @file mention autocomplete ---

// buildMentionItems scans the current directory for files and returns
// autocomplete items. Directories get a trailing slash.
