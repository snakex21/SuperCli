package tui

import (
	"strconv"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/system/config"
	"supercli/internal/system/uilang"
	"supercli/internal/tools/sandbox"
)

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
	case "show_generation_speed":
		c.ShowGenerationSpeed = cycleTri(c.ShowGenerationSpeed)
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
	case "discard_previous_reasoning":
		c.DiscardPreviousReasoning = cycleTri(c.DiscardPreviousReasoning)
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
	case "cost_currency":
		c.CostCurrency = ""
	case "show_generation_speed":
		c.ShowGenerationSpeed = nil
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
	case "discard_previous_reasoning":
		c.DiscardPreviousReasoning = nil
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
	case "orchestrator_model":
		c.OrchestratorModel = ""
	case "compact_model":
		c.CompactModel = ""
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
	case "cost_currency":
		if strings.TrimSpace(c.CostCurrency) == "" {
			return config.EffectiveCostCurrency(*c), "default"
		}
		return config.EffectiveCostCurrency(*c), "manual"
	case "show_generation_speed":
		return m.triDisplay(c.ShowGenerationSpeed, "on")
	case "language":
		language := c.Language
		if language == "" {
			language = m.language
		}
		return uilang.Name(language), "manual"
	case "orchestrator":
		if c.Orchestrator == nil {
			return "auto", "default"
		}
		if *c.Orchestrator {
			return m.tr("tui.setting_value.9cdc6c47aa"), "manual"
		}
		return m.tr("tui.setting_value.6497e4b3d7"), "manual"
	case "allow_all":
		if c.AllowAll {
			return m.settingStateDisplay("on"), "manual"
		}
		return m.settingStateDisplay("off"), "default"
	case "stable_toolset":
		return m.triDisplay(c.StableToolset, "on")
	case "discard_previous_reasoning":
		return m.triDisplay(c.DiscardPreviousReasoning, "off")
	case "thinking":
		v := "on"
		if !llm.ThinkingEnabled() {
			v = "off"
		}
		if c.Thinking == nil {
			return m.settingStateDisplay(v), "default"
		}
		return m.settingStateDisplay(v), "manual"
	case "cache_prompt":
		return m.triAutoDisplay(c.CachePrompt, "on", "off")
	case "darwin_parallel":
		return m.triAutoDisplay(c.DarwinParallel, "parallel", "sequential")
	case "task_parallel":
		return m.triAutoDisplay(c.TaskParallel, "parallel", "sequential")
	case "navigator":
		if strings.TrimSpace(c.Navigator) == "" {
			return "auto", "default"
		}
		return m.settingStateDisplay(c.Navigator), "manual"
	case "memory_briefing_tokens":
		return m.intDisplay(c.MemoryBriefingTokens, m.tr("tui.setting_value.63cbdf9d7d"))
	case "context_policy":
		return m.tr("tui.setting_value.4a9cb686d5"), "built-in"
	case "context_window":
		return m.intDisplay(c.ContextWindow, m.tr("tui.setting_value.1a9561da73")+"auto)")
	case "prune_protect_tokens":
		return m.intDisplay(c.PruneProtectTokens, m.tr("tui.setting_value.98f9ec1cd2"))
	case "task_max_steps":
		return m.intDisplay(c.TaskMaxSteps, m.tr("tui.setting_value.c5d9c16664"))
	case "task_max_tokens":
		return m.intDisplay(int(c.TaskMaxTokens), m.tr("tui.setting_value.5b63281580"))
	case "task_model":
		if strings.TrimSpace(c.TaskModel) == "" {
			return m.tr("tui.setting_value.8119d30029"), "default"
		}
		return c.TaskModel, "manual"
	case "orchestrator_model":
		if strings.TrimSpace(c.OrchestratorModel) == "" {
			return m.tr("tui.setting_value.c0d7b51675"), "default"
		}
		return c.OrchestratorModel, "manual"
	case "compact_model":
		if strings.TrimSpace(c.CompactModel) == "" {
			return m.tr("tui.setting_value.f5c2d04208"), "default"
		}
		return c.CompactModel, "manual"
	case "fallback_models":
		if len(c.FallbackModels) == 0 {
			return m.tr("tui.setting_value.1b0ad2a0d6"), "default"
		}
		return strings.Join(c.FallbackModels, " ; "), "manual"
	case "fallback_cooldown_seconds":
		return m.intDisplay(c.FallbackCooldownSeconds, m.tr("tui.setting_value.1a9561da73")+"30)")
	case "draft_verify":
		return m.triDisplay(c.DraftVerify, "off")
	case "noop_gate":
		return m.triDisplay(c.NoopGate, "off")
	case "preflight_repo":
		return m.triDisplay(c.PreflightRepo, "on")
	case "draft_verify_max_rounds":
		return m.intDisplay(c.DraftVerifyMaxRounds, m.tr("tui.setting_value.1a9561da73")+"2)")
	case "verify_commands":
		if len(c.VerifyCommands) == 0 {
			return m.tr("tui.setting_value.b050b79da0"), "default"
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
func (m Model) triDisplay(p *bool, def string) (value, source string) {
	if p == nil {
		return m.settingStateDisplay(def), "default"
	}
	if *p {
		return m.settingStateDisplay("on"), "manual"
	}
	return m.settingStateDisplay("off"), "manual"
}

// triAutoDisplay renders a tri-state whose nil means host-dependent auto.
func (m Model) triAutoDisplay(p *bool, on, off string) (value, source string) {
	if p == nil {
		return "auto", "default"
	}
	if *p {
		return m.settingStateDisplay(on), "manual"
	}
	return m.settingStateDisplay(off), "manual"
}

func (m Model) intDisplay(v int, def string) (value, source string) {
	if v == 0 {
		return def, "default"
	}
	return strconv.Itoa(v), "manual"
}

// Only known switch states are localized. Provider names, model identifiers
// and user-entered commands pass straight from their own setting branches.
func (m Model) settingStateDisplay(state string) string {
	switch state {
	case "on":
		return m.tr("tui.setting_value.b8d31e8527")
	case "off":
		return m.tr("tui.setting_value.b4dc66dde8")
	case "parallel":
		return m.tr("tui.setting_value.83a00300ad")
	case "sequential":
		return m.tr("tui.setting_value.3ee15b8a77")
	default:
		return state
	}
}

func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
