package webgui

import (
	"fmt"
	"strconv"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/system/config"
	"supercli/internal/tools/sandbox"
)

func knobSet(c *config.TomlConfig, key, val string) error {
	val = strings.TrimSpace(val)
	setTri := func(p **bool) error {
		switch val {
		case "default", "auto", "":
			*p = nil
		case "on":
			v := true
			*p = &v
		case "off":
			v := false
			*p = &v
		default:
			return fmt.Errorf("bad value %q (want on/off/default)", val)
		}
		return nil
	}
	setInt := func(dst *int) error {
		if val == "" {
			*dst = 0
			return nil
		}
		n, err := strconv.Atoi(val)
		if err != nil || n < 0 {
			return fmt.Errorf("bad value %q (want a non-negative integer)", val)
		}
		*dst = n
		return nil
	}
	switch key {
	case "orchestrator":
		return setTri(&c.Orchestrator)
	case "allow_all":
		switch val {
		case "on":
			c.AllowAll = true
		case "off", "default", "auto", "":
			c.AllowAll = false
		default:
			return fmt.Errorf("bad value %q (want on/off)", val)
		}
		sandbox.SetUnsandboxed(c.AllowAll)
		return nil
	case "thinking":
		if err := setTri(&c.Thinking); err != nil {
			return err
		}
		llm.SetThinkingEnabled(c.Thinking == nil || *c.Thinking)
		return nil
	case "navigator":
		switch val {
		case "auto", "default", "":
			c.Navigator = ""
		case "on", "off":
			c.Navigator = val
		default:
			return fmt.Errorf("bad value %q (want auto/on/off)", val)
		}
		return nil
	case "stable_toolset":
		return setTri(&c.StableToolset)
	case "cache_prompt":
		if err := setTri(&c.CachePrompt); err != nil {
			return err
		}
		llm.SetCachePromptDefault(c.CachePrompt)
		return nil
	case "darwin_parallel":
		return setTri(&c.DarwinParallel)
	case "task_parallel":
		return setTri(&c.TaskParallel)
	case "memory_briefing_tokens":
		return setInt(&c.MemoryBriefingTokens)
	case "task_max_steps":
		return setInt(&c.TaskMaxSteps)
	case "task_max_tokens":
		n := 0
		if err := setInt(&n); err != nil {
			return err
		}
		c.TaskMaxTokens = int64(n)
		return nil
	case "task_model":
		c.TaskModel = val
		return nil
	case "compact_model":
		c.CompactModel = val
		return nil
	case "fallback_models":
		c.FallbackModels = splitList(val)
		return nil
	case "fallback_cooldown_seconds":
		return setInt(&c.FallbackCooldownSeconds)
	case "noop_gate":
		return setTri(&c.NoopGate)
	case "preflight_repo":
		return setTri(&c.PreflightRepo)
	case "draft_verify":
		return setTri(&c.DraftVerify)
	case "draft_verify_max_rounds":
		return setInt(&c.DraftVerifyMaxRounds)
	case "verify_commands":
		c.VerifyCommands = splitList(val)
		return nil
	}
	return fmt.Errorf("unknown or read-only setting %q", key)
}

func splitList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ";") {
		if item := strings.TrimSpace(part); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// knobResetAll returns every managed knob to its default. Provider
// entries, API keys, hidden models and default_model/provider are left
// untouched — identical to the TUI's "reset all".
func knobResetAll(c *config.TomlConfig) {
	for _, d := range knobDefs() {
		if d.kind == knobReadonly {
			continue
		}
		_ = knobSet(c, d.key, "")
	}
}

// handleConfigKnobs is GET/POST /api/config: the /settings panel.
//
//	GET  -> {"knobs":[...]}
//	POST {"key":K,"value":V} -> set one knob ("" or "default" resets)
//	POST {"reset_all":true}  -> reset every managed knob
//
// Writes go to the GLOBAL config.toml (same file the TUI /settings,
// /think and /orchestrator persist to), loaded fresh per request so
// provider entries and keys written by other surfaces survive.
