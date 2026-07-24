package app

import (
	"path/filepath"

	"supercli/internal/account/tier"
	"supercli/internal/agent"
	"supercli/internal/agent/reflect"
	"supercli/internal/llm"
	"supercli/internal/llm/draft"
	"supercli/internal/llm/factory"
	"supercli/internal/system/config"
)

// draftReflectBundle is F11 draft wiring + F5.a reflection.
type draftReflectBundle struct {
	Policy             *draft.Policy
	Provider           llm.Provider
	Sink               agent.DraftOverrideSink
	ReflectEvery       int
	AdaptiveReflection bool
	Reflector          agent.Reflector
}

// wireDraftAndReflection builds the draft policy/provider/sink and the
// mid-run reflector from flags + config. Half-configured draft is off.
func wireDraftAndReflection(
	draftModeFlag, draftModelFlag string,
	provider llm.Provider,
	provFactory *factory.Factory,
	cfg config.Config,
	tierRules []tier.Rule,
	tomlCfg config.TomlConfig,
	dataDir string,
) draftReflectBundle {
	// F11 draft wiring. Build the draft policy +
	// provider + sink + stats only when F11 is not
	// explicitly off. The draft model is picked
	// from the F16 CapabilityRegistry's
	// SuggestCheapestForTask("plan") — never
	// hardcoded in Go (D1 decision).
	// The draft provider comes metered from the factory (default
	// purpose "draft"); its other roles (navigator side provider,
	// memory summarizer) re-label per call via llm.WithPurpose.
	var b draftReflectBundle
	b.Policy, b.Provider = buildDraftWiring(draftModeFlag, draftModelFlag, provider, provFactory, cfg, tierRules)
	if b.Provider != nil {
		b.Sink = reflect.NewJSONLDraftOverrideSink(filepath.Join(dataDir, "reflect"))
	}

	// F5.a: mid-run reflection. The default is adaptive: it spends the
	// extra model call only after repeated tool failures, an identical
	// tool-call batch, or just before MaxSteps. An explicit positive
	// reflect_every preserves fixed periodic checkpoints; negative disables.
	b.ReflectEvery = 8
	b.AdaptiveReflection = tomlCfg.ReflectEvery == 0
	if tomlCfg.ReflectEvery != 0 {
		b.ReflectEvery = tomlCfg.ReflectEvery
	}
	if b.ReflectEvery >= 0 {
		b.Reflector = &reflect.ModelReflector{Provider: provider}
	}
	return b
}
