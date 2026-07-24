package tui

import (
	"strconv"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/system/config"
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
		return "prune 60% · compact window − reserve", "built-in"
	case "context_window":
		return intDisplay(c.ContextWindow, "auto")
	case "prune_protect_tokens":
		return intDisplay(c.PruneProtectTokens, "scaled")
	case "task_max_steps":
		return intDisplay(c.TaskMaxSteps, "spec or 10")
	case "task_max_tokens":
		return intDisplay(int(c.TaskMaxTokens), "no cap")
	case "task_model":
		if strings.TrimSpace(c.TaskModel) == "" {
			return "default (coordinator's model)", "default"
		}
		return c.TaskModel, "manual"
	case "compact_model":
		if strings.TrimSpace(c.CompactModel) == "" {
			return "default (active model)", "default"
		}
		return c.CompactModel, "manual"
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
