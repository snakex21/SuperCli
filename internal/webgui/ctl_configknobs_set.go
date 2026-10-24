package webgui

import (
	"fmt"
	"strconv"
	"strings"

	"supercli/internal/account/fx"
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
	case "discard_previous_reasoning":
		return setTri(&c.DiscardPreviousReasoning)
	case "show_generation_speed":
		return setTri(&c.ShowGenerationSpeed)
	case "update_notify":
		return setTri(&c.UpdateNotify)
	case "assistant_edit":
		return setTri(&c.AssistantEdit)
	case "assistant_edit_model_sees":
		return setTri(&c.AssistantEditModelSees)
	case "cost_currency":
		if val == "" || strings.EqualFold(val, "default") {
			c.CostCurrency = ""
			return nil
		}
		code, err := fx.NormalizeCurrency(val)
		if err != nil {
			return err
		}
		c.CostCurrency = code
		return nil
	case "thinking":
		if err := setTri(&c.Thinking); err != nil {
			return err
		}
		llm.SetThinkingEnabled(c.Thinking == nil || *c.Thinking)
		return nil
	case "queue_merge":
		switch val {
		case "", "default", "off":
			c.QueueMerge = "off"
		case "auto":
			c.QueueMerge = "auto"
		default:
			return fmt.Errorf("bad value %q (want off/auto)", val)
		}
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
	case "darwin_models":
		c.DarwinModels = splitList(val)
		return nil
	case "darwin_judge_model":
		c.DarwinJudgeModel = val
		return nil
	case "darwin_pool_size":
		if strings.EqualFold(val, "auto") || strings.EqualFold(val, "default") {
			val = ""
		}
		n := 0
		if err := setInt(&n); err != nil {
			return err
		}
		if n > 10 {
			return fmt.Errorf("bad value %q (want 0-10; 0 = auto)", val)
		}
		c.DarwinPoolSize = n
		return nil
	case "darwin_auto_merge":
		return setTri(&c.DarwinAutoMerge)
	case "darwin":
		return setTri(&c.Darwin)
	case "task_parallel":
		return setTri(&c.TaskParallel)
	case "context_window":
		return setInt(&c.ContextWindow)
	case "prune_protect_tokens":
		return setInt(&c.PruneProtectTokens)
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
	case "task_reasoning_effort", "advisor_reasoning_effort", "review_reasoning_effort", "darwin_reasoning_effort":
		val = strings.ToLower(val)
		if val == "default" {
			val = ""
		}
		if err := llm.ValidateReasoningEffort(val); err != nil {
			return err
		}
		switch key {
		case "task_reasoning_effort":
			c.TaskReasoningEffort = val
		case "advisor_reasoning_effort":
			c.AdvisorReasoningEffort = val
		case "review_reasoning_effort":
			c.ReviewReasoningEffort = val
		case "darwin_reasoning_effort":
			c.DarwinReasoningEffort = val
		}
		return nil
	case "advisor_model":
		c.AdvisorModel = val
		return nil
	case "review_model":
		c.ReviewModel = val
		return nil
	case "orchestrator_model":
		c.OrchestratorModel = val
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
