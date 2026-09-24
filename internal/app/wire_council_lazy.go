package app

import (
	"sync"

	"supercli/internal/llm"
	"supercli/internal/llm/consult"
	"supercli/internal/llm/factory"
	"supercli/internal/system/config"
)

// newConsultCouncil leaves the optional sample pool unbuilt until it is used.
// Listing tools, ordinary chat and an explicit council roster need only the
// judge. Once resolved, samples are shared by concurrent and subsequent calls.
func newConsultCouncil(n int, judge llm.Provider, caps *llm.CapabilityRegistry, cfg config.Config, f *factory.Factory) *consult.Council {
	return &consult.Council{
		Judge: judge,
		LoadSamples: sync.OnceValue(func() []llm.Provider {
			if council := buildConsultCouncil(n, judge, caps, cfg, f); council != nil {
				return council.Samples
			}
			return nil
		}),
	}
}
