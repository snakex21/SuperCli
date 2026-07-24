package app

import (
	"log"

	"supercli/internal/account/tier"
	"supercli/internal/llm"
	"supercli/internal/llm/draft"
	"supercli/internal/llm/factory"
	"supercli/internal/system/config"
)

// buildDraftWiring assembles the F11 draft policy +
// provider. The provider is a SECOND llm.OpenAI
// instance (or llm.Echo when the verifier is echo)
// configured with a different Model id.
//
// F11 is OPT-IN. It engages only when BOTH:
//   - a draft mode other than "off" is set, AND
//   - a draft model is EXPLICITLY configured via
//     --draft-model (or config.toml draft_model).
//
// There is deliberately no auto-pick: an unset draft
// model means no speculative decoding. This avoids
// silently engaging a draft model the user never chose.
//
// Returns (nil, nil) when F11 is off or no draft model
// was configured — silent fallback per D1.
func buildDraftWiring(modeFlag, modelFlag string, verifier llm.Provider, f *factory.Factory, cfg config.Config, tierRules []tier.Rule) (*draft.Policy, llm.Provider) {
	mode, err := draft.ParseMode(modeFlag)
	if err != nil {
		log.Printf("F11: bad --draft-mode %q, defaulting to off: %v", modeFlag, err)
		return nil, nil
	}
	if mode == draft.ModeOff {
		return nil, nil
	}
	verifierName := verifier.Name()
	// F11 is OPT-IN: it engages only when the user has
	// EXPLICITLY configured a draft model (via --draft-model
	// or config.toml draft_model). We deliberately do NOT
	// auto-pick a draft model from the F16 registry — an
	// unset draft model means "no speculative decoding",
	// even if a mode was supplied.
	if modelFlag == "" {
		log.Printf("F11: no draft model configured (--draft-model unset); F11 disabled (opt-in)")
		return nil, nil
	}
	draftModel := modelFlag
	if draftModel == verifierName {
		log.Printf("F11: draft model %q == verifier; F11 disabled silently (per D1 rule)", draftModel)
		return nil, nil
	}
	policy, err := draft.NewPolicy(mode, draftModel, verifierName, nil)
	if err != nil {
		log.Printf("F11: policy build failed: %v; F11 disabled silently", err)
		return nil, nil
	}
	// Build a second provider instance with the same
	// transport but a different model id. The
	// verifier's provider and the draft's provider
	// share API key, base URL, etc. Built through the
	// factory, so the draft comes back metered (default
	// purpose "draft").
	dCfg := cfg
	dCfg.Model = draftModel
	if cfg.IsEcho() {
		// Echo mode: build a separate echo for the
		// draft, which is fine for tests / offline.
		dCfg.Model = "draft:" + draftModel
	}
	prov, err := f.Build(dCfg, llm.PurposeDraft)
	if err != nil {
		log.Printf("F11: draft provider build failed: %v; F11 disabled silently", err)
		return nil, nil
	}
	return policy, prov
}
