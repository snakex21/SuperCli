package llm

import (
	"strings"
	"sync"
)

// toolsEffortRejectedEndpoints remembers, for the life of the process, every
// endpoint+model pair that refused reasoning effort together with function
// tools (gpt-5.4+ on /chat/completions). Provider instances are rebuilt often
// (each batch run, task worker or model re-selection), and without this each
// new instance paid one doomed 400 round trip before its first token.
var toolsEffortRejectedEndpoints sync.Map

func toolsEffortEndpointKey(baseURL, model string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(baseURL)), "/") + "\x00" + model
}

func (p *OpenAIProvider) noteToolsEffortRejected() {
	p.toolsEffortRejected.Store(true)
	toolsEffortRejectedEndpoints.Store(toolsEffortEndpointKey(p.cfg.BaseURL, p.cfg.Model), struct{}{})
}

func (p *OpenAIProvider) toolsEffortKnownRejected() bool {
	if p.toolsEffortRejected.Load() {
		return true
	}
	if _, ok := toolsEffortRejectedEndpoints.Load(toolsEffortEndpointKey(p.cfg.BaseURL, p.cfg.Model)); ok {
		p.toolsEffortRejected.Store(true)
		return true
	}
	return false
}

// resetToolsEffortRejected clears the process-wide memory (tests only).
func resetToolsEffortRejected() {
	toolsEffortRejectedEndpoints.Range(func(k, _ any) bool {
		toolsEffortRejectedEndpoints.Delete(k)
		return true
	})
}
