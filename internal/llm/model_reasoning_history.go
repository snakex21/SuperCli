package llm

import "sync/atomic"

// This changes replay of completed replies, not generation of new reasoning.
var discardPreviousReasoning atomic.Bool

func SetDiscardPreviousReasoning(discard bool) { discardPreviousReasoning.Store(discard) }
func DiscardPreviousReasoning() bool           { return discardPreviousReasoning.Load() }
