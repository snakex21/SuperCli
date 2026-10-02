package agent

import (
	"sync"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// A loop keeps only the latest definition set. Descriptor strings are immutable
// registry values; the provider still owns a new, exactly sized slice per call.
// This saves registry copies and repeated schema estimates without freezing
// activation, requested media tools, route changes or later registrations.
type toolDefinitionSnapshot struct {
	mu     sync.Mutex
	valid  bool
	key    toolDefinitionKey
	defs   []llm.ToolDef
	tokens int
}

type toolDefinitionKey struct {
	registry                              *tools.Registry
	revision                              uint64
	route                                 RouteMode
	thin, stable, orchestrator, finalOnly bool
	screenshot, headless, images          bool
}

func (s *toolDefinitionSnapshot) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.valid = false
	s.key = toolDefinitionKey{}
	s.defs = nil
	s.tokens = 0
}

func (l *Loop) currentToolDefinitionKey() toolDefinitionKey {
	key := toolDefinitionKey{
		registry: l.registry, route: l.route,
		thin: l.thinTools, stable: l.stableToolset,
		orchestrator: l.orchestrator, finalOnly: l.finalReplyOnly,
		screenshot: l.screenshotForRun, headless: l.headlessForRun,
	}
	if key.registry != nil {
		key.revision = key.registry.Revision()
	}
	if !key.finalOnly && key.route != RouteCoordinator {
		key.images = l.hasSessionImages()
	}
	return key
}

// Called with the snapshot mutex held. Snapshot publication is conditional:
// registry visibility may change while
// the uncached builder reads it. Do not label such a set with the old revision.
// The next lookup rebuilds it instead of caching a potentially mixed contract.
func (s *toolDefinitionSnapshot) storeIfCurrent(key toolDefinitionKey, defs []llm.ToolDef, tokens int) bool {
	if key.registry != nil && key.registry.Revision() != key.revision {
		return false
	}
	s.key, s.defs, s.tokens, s.valid = key, defs, tokens, true
	return true
}

func (l *Loop) preparedToolDefinitions(wire bool) ([]llm.ToolDef, int) {
	snapshot := &l.toolDefsSnapshot
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	key := l.currentToolDefinitionKey()
	defs, tokens := snapshot.defs, snapshot.tokens
	if !snapshot.valid || snapshot.key != key {
		defs = l.buildToolDefsUncached()
		tokens = estimateRequestTokens(nil, defs)
		snapshot.storeIfCurrent(key, defs, tokens)
	}
	if !wire || defs == nil {
		return nil, tokens
	}
	owned := make([]llm.ToolDef, len(defs))
	copy(owned, defs)
	return owned, tokens
}

func (l *Loop) buildToolDefs() []llm.ToolDef {
	defs, _ := l.preparedToolDefinitions(true)
	return defs
}

// Estimation needs only the cached cost, not an allocated provider slice.
// completeOnce still prices the actual passed snapshot, whose revision may
// differ from the registry after a tool activation or a concurrent update.
func (l *Loop) toolDefinitionTokens() int {
	_, tokens := l.preparedToolDefinitions(false)
	return tokens
}
