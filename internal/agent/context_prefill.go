package agent

import (
	"time"

	"supercli/internal/llm"
)

const prefillProfileDefaultScope = "default"

func (l *Loop) prefillScope() string {
	if l.contextProvider != "" {
		return l.contextProvider
	}
	return prefillProfileDefaultScope
}

// adaptivePrefillBudget returns a learned total-prompt threshold. The ordinary
// context threshold remains the hard safety cap; a profile can only lower it.
func (l *Loop) adaptivePrefillBudget(hardThreshold int) (int, bool) {
	if l.prefillProfiles == nil {
		return 0, false
	}
	return l.prefillProfiles.Budget(l.prefillScope(), l.modelID, hardThreshold)
}

// effectiveCompactThreshold combines correctness (model window minus output
// reserve) with measured performance (prompt size that meets the TTFT target).
func (l *Loop) effectiveCompactThreshold(hardThreshold int, hardSource string) (int, string) {
	if budget, ok := l.adaptivePrefillBudget(hardThreshold); ok {
		return budget, "prefill-profile"
	}
	return hardThreshold, hardSource
}

// observePrefillCall feeds only successful main-loop calls into the shared
// profile. The request estimate is the fallback for providers that omit usage.
func (l *Loop) observePrefillCall(requestEstimate int, usage *llm.Usage, ttft time.Duration) {
	if l.prefillProfiles == nil || ttft <= 0 {
		return
	}
	input, cached := requestEstimate, 0
	if usage != nil {
		if usage.Input > 0 {
			input = usage.Input
		}
		cached = usage.CachedInput
	}
	l.prefillProfiles.Observe(l.prefillScope(), l.modelID, llm.PrefillSample{
		InputTokens: input, CachedTokens: cached, TTFT: ttft,
	})
}

// PrefillBudget reports the active learned threshold for diagnostics.
func (l *Loop) PrefillBudget() (int, bool) {
	return l.adaptivePrefillBudget(autoCompactThreshold(l.window()))
}

// PrefillProfile returns the learned profile used by /context diagnostics.
func (l *Loop) PrefillProfile() (llm.PrefillProfile, bool) {
	if l == nil || l.prefillProfiles == nil {
		return llm.PrefillProfile{}, false
	}
	return l.prefillProfiles.Profile(l.prefillScope(), l.modelID)
}

// PrefillProfiles exposes the shared store to delegated child loops without
// exposing any endpoint credentials or prompt content.
func (l *Loop) PrefillProfiles() *llm.PrefillProfiles {
	if l == nil {
		return nil
	}
	return l.prefillProfiles
}
