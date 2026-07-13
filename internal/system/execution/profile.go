// Package execution resolves one stable model/backend execution profile.
//
// Model capability and backend speed are deliberately separate axes: a large
// model can still run on a slow local GPU, while a small model can be served by
// a fast cloud endpoint.  Front-ends consume the same resolved profile so TUI,
// WebGUI and batch do not silently send different prompt/tool contracts.
package execution

import (
	"strings"

	"supercli/internal/account/tier"
	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// Profile is frozen when an agent loop is created. Keeping it stable for the
// entire loop is important for provider-side KV prompt caches.
type Profile struct {
	Capability            tier.Tier
	LocalBackend          bool
	PromptSmall           bool
	ThinTools             bool
	StableToolset         bool
	EnableNavigator       bool
	NavigatorAuto         bool
	NavigatorKeywordsOnly bool
	CatalogHoist          bool
}

// Resolve classifies model capability independently from the host. Small
// models use the compact prompt/tool protocol for comprehension and context
// economy. Large models keep the richer prompt, but large models on a local or
// private host still use the compact tool catalog because prefill is usually
// the bottleneck there. small_full_tools remains the explicit escape hatch.
func Resolve(cfg config.Config, tc config.TomlConfig, caps *llm.CapabilityRegistry, catalogHoist bool) Profile {
	var inputCost, outputCost float64
	if caps != nil {
		if info, ok := caps.Get(cfg.Model); ok {
			inputCost, outputCost = info.InputCost, info.OutputCost
		}
	}
	capability := tier.Classify(cfg.Model, inputCost, outputCost, TierRules(tc))
	local := llm.IsLocalBaseURL(cfg.BaseURL)
	enableNavigator, navigatorAuto := Navigator(tc.Navigator)
	thinTools := !tc.SmallFullTools && (capability == tier.Small || local)
	stableToolset := StableToolset(tc.StableToolset)
	return Profile{
		Capability:            capability,
		LocalBackend:          local,
		PromptSmall:           capability == tier.Small,
		ThinTools:             thinTools,
		StableToolset:         stableToolset,
		EnableNavigator:       enableNavigator,
		NavigatorAuto:         navigatorAuto,
		NavigatorKeywordsOnly: navigatorAuto,
		// HP Z6/Qwen3.5-122B live A/B measured 1506 fewer evaluated
		// tokens across two warm turns (1743 -> 237) with both quality
		// arms passing. Hoist is therefore the default whenever the thin
		// catalog and stable toolset make it safe; the legacy env flag can
		// still force the experiment for other profiles.
		CatalogHoist: catalogHoist || (thinTools && stableToolset),
	}
}

// Navigator maps config navigator=off|on|auto to loop flags. Unknown values
// degrade to the safe and economical keyword-first auto mode.
func Navigator(mode string) (enable, auto bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "off":
		return false, false
	case "on":
		return true, false
	default:
		return true, true
	}
}

// StableToolset resolves the tri-state knob. Schema stability is the default
// because changing the tools prefix between turns invalidates local KV caches
// and cloud prompt caches alike.
func StableToolset(override *bool) bool {
	if override != nil {
		return *override
	}
	return true
}

// Parallel resolves a backend-aware parallelism knob. A single local GPU
// should run delegated model calls sequentially; cloud backends benefit from
// concurrency. An explicit config value always wins.
func Parallel(baseURL string, override *bool) (enabled, warnLocal bool) {
	local := llm.IsLocalBaseURL(baseURL)
	enabled = !local
	if override != nil {
		enabled = *override
	}
	return enabled, enabled && local
}

// TierRules converts the on-disk override shape once for every front-end.
func TierRules(tc config.TomlConfig) []tier.Rule {
	if len(tc.ModelTiers) == 0 {
		return nil
	}
	out := make([]tier.Rule, 0, len(tc.ModelTiers))
	for _, rule := range tc.ModelTiers {
		out = append(out, tier.Rule{Pattern: rule.Pattern, Tier: rule.Tier})
	}
	return out
}
