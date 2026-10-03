package tui

import (
	"supercli/internal/system/config"
)

// settingKind classifies how a /settings row is edited.
type settingKind int

const (
	setTriState  settingKind = iota // tri-state *bool (default/auto → on → off → default)
	setNavigator                    // navigator string (default auto → on → off → default)
	setInt                          // integer knob (0 = built-in default)
	setText                         // free-text string knob (empty = clear/default)
	setReadonly                     // display only (changed elsewhere, e.g. /model)
	setLanguage                     // shared UI language picker
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
	return []settingRow{
		{"show_generation_speed", textFor(language, "tui.setting_show_generation_speed.label"), textFor(language, "tui.setting_show_generation_speed.desc"), setTriState, false},
		{"language", textFor(language, "tui.setting_language.8027f432a7"), textFor(language, "tui.setting_language.99a2af69fc"), setLanguage, false},
		{"orchestrator", textFor(language, "tui.setting_orchestrator.0a4b20425b"), textFor(language, "tui.setting_orchestrator.4b3142f61d"), setTriState, true},
		{"navigator", textFor(language, "tui.setting_navigator.f4b3f6314f"), textFor(language, "tui.setting_navigator.117b57b29d"), setNavigator, true},
		{"task_parallel", textFor(language, "tui.setting_task_parallel.6bb8136641"), textFor(language, "tui.setting_task_parallel.33c09ad488"), setTriState, true},
		{"task_model", textFor(language, "tui.setting_task_model.7488f3d1c7"), textFor(language, "tui.setting_task_model.993b9e80db"), setText, true},
		{"orchestrator_model", textFor(language, "tui.setting_orchestrator_model.99a9adf37c"), textFor(language, "tui.setting_orchestrator_model.976aa9f04e"), setText, true},
		{"compact_model", textFor(language, "tui.setting_compact_model.a97ce991de"), textFor(language, "tui.setting_compact_model.6f3046be32"), setText, true},
		{"task_max_steps", textFor(language, "tui.setting_task_max_steps.8214ac6f43"), textFor(language, "tui.setting_task_max_steps.6bacd7f7a5"), setInt, true},
		{"task_max_tokens", textFor(language, "tui.setting_task_max_tokens.9b54812a40"), textFor(language, "tui.setting_task_max_tokens.83fd4c61c6"), setInt, true},
		{"darwin_parallel", textFor(language, "tui.setting_darwin_parallel.b039de2a33"), textFor(language, "tui.setting_darwin_parallel.b00873ce9c"), setTriState, true},
		{"draft_verify", textFor(language, "tui.setting_draft_verify.3aaf10a735"), textFor(language, "tui.setting_draft_verify.d7a874f8fd"), setTriState, true},
		{"draft_verify_max_rounds", textFor(language, "tui.setting_draft_verify_max_rounds.a0afb48721"), textFor(language, "tui.setting_draft_verify_max_rounds.8fc2a7faa9"), setInt, true},
		{"verify_commands", textFor(language, "tui.setting_verify_commands.cab6203fa7"), textFor(language, "tui.setting_verify_commands.7451ed04b9"), setText, true},
		{"discard_previous_reasoning", textFor(language, "tui.setting_discard_previous_reasoning.ac817613f8"), textFor(language, "tui.setting_discard_previous_reasoning.9f6743d90f"), setTriState, false},
		{"thinking", textFor(language, "tui.setting_thinking.9d4064367e"), textFor(language, "tui.setting_thinking.f6b8c8b24c"), setTriState, false},
		{"stable_toolset", textFor(language, "tui.setting_stable_toolset.aa16170ae3"), textFor(language, "tui.setting_stable_toolset.dc7da2966d"), setTriState, true},
		{"cache_prompt", textFor(language, "tui.setting_cache_prompt.eb76e6335e"), textFor(language, "tui.setting_cache_prompt.4d7069d01d"), setTriState, true},
		{"context_policy", textFor(language, "tui.setting_context_policy.a8d279895b"), textFor(language, "tui.setting_context_policy.15c6e8ea74"), setReadonly, false},
		{"context_window", textFor(language, "tui.setting_context_window.c36373a149"), textFor(language, "tui.setting_context_window.bf6aee55eb"), setInt, true},
		{"prune_protect_tokens", textFor(language, "tui.setting_prune_protect_tokens.71c84545d0"), textFor(language, "tui.setting_prune_protect_tokens.b7764ffd33"), setInt, true},
		{"memory_briefing_tokens", textFor(language, "tui.setting_memory_briefing_tokens.860b78d6d8"), textFor(language, "tui.setting_memory_briefing_tokens.c04e6a1d05"), setInt, true},
		{"preflight_repo", textFor(language, "tui.setting_preflight_repo.87b593f4d4"), textFor(language, "tui.setting_preflight_repo.b0604569e8"), setTriState, true},
		{"fallback_models", textFor(language, "tui.setting_fallback_models.9c410e8b4f"), textFor(language, "tui.setting_fallback_models.649acf7419"), setText, true},
		{"fallback_cooldown_seconds", textFor(language, "tui.setting_fallback_cooldown_seconds.d70101b0dd"), textFor(language, "tui.setting_fallback_cooldown_seconds.071ab161ca"), setInt, true},
		{"allow_all", textFor(language, "tui.setting_allow_all.adad46e596"), textFor(language, "tui.setting_allow_all.77ea1b1074"), setTriState, false},
		{"noop_gate", textFor(language, "tui.setting_noop_gate.236d6e69d9"), textFor(language, "tui.setting_noop_gate.a1c16ea2ae"), setTriState, false},
		{"default_model", textFor(language, "tui.menu_providers_render.3840d9d294"), textFor(language, "tui.setting_default_model.eaeea4b67b"), setReadonly, false},
		{"default_provider", textFor(language, "tui.setting_default_provider.cf5461dc69"), textFor(language, "tui.setting_default_provider.8d096c44dd"), setReadonly, false},
		{"", textFor(language, "tui.setting_.a04d9bea32"), textFor(language, "tui.setting_.17cf65d4ba"), setResetAll, false},
	}
}

func englishSettingsRows() []settingRow { return settingsRowsFor("en") }

func settingCategory(key string) int {
	switch key {
	case "orchestrator", "task_parallel", "task_model", "orchestrator_model", "task_max_steps", "task_max_tokens":
		return 1
	case "discard_previous_reasoning", "compact_model", "context_policy", "context_window", "memory_briefing_tokens", "preflight_repo", "fallback_models", "fallback_cooldown_seconds":
		return 2
	case "show_generation_speed", "language", "default_model", "default_provider", "allow_all", "":
		return 0
	default:
		return 3
	}
}
func (m Model) settingsCategories() []string {
	return []string{m.tr("tui.categories.c910d474dc"), m.tr("tui.categories.279b44d2ab"), m.tr("tui.categories.a6e600a10f"), m.tr("tui.categories.9f088dbebd")}
}
func (m Model) localizedSettingsRows() []settingRow {
	var rows []settingRow
	for _, row := range settingsRowsFor(m.language) {
		if settingCategory(row.key) == m.menu.category {
			rows = append(rows, row)
		}
	}
	return rows
}

func settingSection(key string) string {
	switch key {
	case "orchestrator", "navigator", "task_parallel", "task_model", "orchestrator_model", "task_max_steps", "task_max_tokens", "darwin_parallel", "draft_verify", "draft_verify_max_rounds", "verify_commands":
		return textFor("pl", "tui.setting_section.86e566d0ea")
	case "discard_previous_reasoning", "thinking", "stable_toolset", "cache_prompt", "context_policy", "context_window", "prune_protect_tokens", "memory_briefing_tokens", "preflight_repo", "fallback_models", "fallback_cooldown_seconds", "compact_model":
		return textFor("pl", "tui.setting_section.630226b61c")
	default:
		return textFor("pl", "tui.action_cost.6725e7bbcd")
	}
}

func (m Model) localizedSettingSection(key string) string {
	switch key {
	case "orchestrator", "navigator", "task_parallel", "task_model", "orchestrator_model", "task_max_steps", "task_max_tokens", "darwin_parallel", "draft_verify", "draft_verify_max_rounds", "verify_commands":
		return m.tr("tui.setting_section.86e566d0ea")
	case "discard_previous_reasoning", "thinking", "stable_toolset", "cache_prompt", "context_policy", "context_window", "prune_protect_tokens", "memory_briefing_tokens", "preflight_repo", "fallback_models", "fallback_cooldown_seconds", "compact_model":
		return m.tr("tui.setting_section.630226b61c")
	default:
		return m.tr("tui.action_cost.6725e7bbcd")
	}
}

// settingsGlobalPath returns the global config.toml path — the same file
// /think and /orchestrator persist to.
func (m Model) settingsGlobalPath() string {
	global, _ := config.FindTomlPaths(m.dataDir, m.home)
	return global
}

// openSettingsMenu loads the global config and opens the /settings panel.
