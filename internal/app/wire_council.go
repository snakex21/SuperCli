package app

import (
	"log"

	"supercli/internal/llm"
	"supercli/internal/llm/consult"
	"supercli/internal/llm/factory"
	"supercli/internal/llm/providers"
	"supercli/internal/system/config"
	"supercli/internal/tools"
)

// councilPickerOptions builds the option list for the
// /council roster picker. Preferred source: models the
// provider scanner discovered per configured provider
// (labels "providerName/modelID" — buildable directly).
// Fallback when nothing has been scanned yet: every
// visible model id from the capability registry (bare
// ids; the active transport serves them). Hidden models
// are skipped; the list is capped to keep the picker
// usable.
func councilPickerOptions(provMgr *providers.Manager, caps *llm.CapabilityRegistry) []tools.AskOption {
	const maxOpts = 40
	var opts []tools.AskOption
	seen := make(map[string]struct{})
	for _, pi := range provMgr.ListConfigured(caps) {
		for _, mi := range pi.Models {
			if provMgr.IsHiddenFor(pi.Name, mi.ID) {
				continue
			}
			label := pi.Name + "/" + mi.ID
			if _, ok := seen[label]; ok {
				continue
			}
			seen[label] = struct{}{}
			opts = append(opts, tools.AskOption{Label: label, Description: pi.BaseURL})
		}
	}
	if len(opts) == 0 && caps != nil {
		for _, mi := range caps.All() {
			if provMgr.IsHiddenFor(mi.Provider, mi.ID) {
				continue
			}
			opts = append(opts, tools.AskOption{Label: mi.ID, Description: mi.Provider})
		}
	}
	if len(opts) > maxOpts {
		opts = opts[:maxOpts]
	}
	return opts
}

// buildConsultCouncil assembles the F12 cross-model
// consultation engine. The samples are N model ids
// pulled from the F16 CapabilityRegistry
// (SuggestCheapestN, cheapest-first, excluding the
// judge itself). The judge is the running main
// provider (the user is already paying for it).
//
// Samples use the SAME transport as the judge: chat-completions and
// Responses providers are rebuilt with a different model id while sharing
// the user's API key + base URL. When the user
// later adds Anthropic / Ollama / Groq adapters
// (F15 territory), the per-provider factory will
// branch on caps.Provider(id).
//
// Returns nil when the registry is empty, when no
// candidates are available, or when provider
// construction fails for every id. The consult
// tool and the /council slash command both
// gracefully degrade to "not wired" in that case.
func buildConsultCouncil(n int, judge llm.Provider, caps *llm.CapabilityRegistry, cfg config.Config, f *factory.Factory) *consult.Council {
	if n <= 0 {
		n = 3
	}
	if caps == nil || judge == nil || f == nil {
		return nil
	}
	judgeName := judge.Name()
	ids := caps.SuggestCheapestN("consult", judgeName, n)
	if len(ids) == 0 {
		log.Printf("F12: no sample models available (judge=%q); consult disabled", judgeName)
		return nil
	}
	samples := make([]llm.Provider, 0, len(ids))
	for _, id := range ids {
		// Same transport as the judge, different model id — built
		// through the factory so sample calls are metered under
		// "consult" like every other model call in the process.
		sCfg := cfg
		sCfg.Model = id
		if cfg.IsEcho() {
			sCfg.Model = "consult-sample:" + id
		}
		prov, err := f.Build(sCfg, llm.PurposeConsult)
		if err != nil {
			log.Printf("F12: sample provider %q build failed: %v", id, err)
			continue
		}
		if prov != nil {
			samples = append(samples, prov)
		}
	}
	if len(samples) == 0 {
		log.Printf("F12: no sample providers built; consult disabled")
		return nil
	}
	log.Printf("F12: council assembled: %d sample(s) (judge=%q)", len(samples), judgeName)
	return &consult.Council{
		Samples: samples,
		Judge:   judge,
	}
}
