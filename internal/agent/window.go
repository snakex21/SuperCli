package agent

import (
	"strings"

	"supercli/internal/llm"
)

// defaultContextWindow is the conservative fallback when the
// model's context window cannot be resolved from config,
// provider metadata, or learned limits.
const defaultContextWindow = 16384

// Automatic compaction keeps an explicit generation reserve instead of
// throwing away a fixed fraction of every context window. A percentage-only
// policy wastes hundreds of thousands of useful tokens on million-token
// models, while a fixed reserve is too large for small local models.
const (
	minContextReserve = 2048
	maxContextReserve = 65536
)

func autoCompactThreshold(window int) int {
	if window <= 0 {
		return 0
	}
	reserve := window / 10
	if reserve < minContextReserve {
		reserve = minContextReserve
	}
	if reserve > maxContextReserve {
		reserve = maxContextReserve
	}
	// Tiny test/custom windows still need a usable positive threshold.
	if reserve >= window {
		reserve = window / 8
		if reserve < 1 {
			reserve = 1
		}
	}
	return window - reserve
}

// AutoCompactThreshold exposes the effective trigger for status UIs and
// diagnostics so they never drift from the policy used by the run loop.
func AutoCompactThreshold(window int) int { return autoCompactThreshold(window) }

// A generated summary must remove at least 20% of the prefix it replaces.
// Otherwise accepting it spends context while also discarding exact history.
// The threshold mirrors the proven guard in grok-build and is deliberately
// fixed: it is a safety invariant, not another user-facing tuning knob.
const maxCompactionRatio = 0.8

// ContextWindowResolution records both the limit and why SuperCli trusts it.
// The source is intentionally a short stable string because it is emitted in
// TUI/WebGUI/batch telemetry and may be grepped from session logs.
type ContextWindowResolution struct {
	Tokens int
	Source string
}

// ResolveContextWindow applies the shared front-end cascade. providerTokens is
// the optional live /v1/models result; callers that do not have one pass zero.
func ResolveContextWindow(model string, configured, providerTokens int, caps *llm.CapabilityRegistry, learned *llm.LearnedLimits) ContextWindowResolution {
	if configured > 0 {
		return ContextWindowResolution{Tokens: configured, Source: "config"}
	}
	if providerTokens > 0 {
		return ContextWindowResolution{Tokens: providerTokens, Source: "provider"}
	}
	if caps != nil {
		if info, ok := caps.Get(model); ok && info.ContextLength > 0 {
			return ContextWindowResolution{Tokens: info.ContextLength, Source: "catalog"}
		}
		// Router providers often use a short active id while catalogs store a
		// canonical provider/model id. Accept only a unique suffix match so two
		// providers advertising the same short id can never select each other.
		short := model
		if slash := strings.LastIndexByte(short, '/'); slash >= 0 {
			short = short[slash+1:]
		}
		matched := 0
		matches := 0
		for _, info := range caps.All() {
			candidate := info.ID
			if slash := strings.LastIndexByte(candidate, '/'); slash >= 0 {
				candidate = candidate[slash+1:]
			}
			if strings.EqualFold(candidate, short) && info.ContextLength > 0 {
				matches++
				if matches > 1 {
					matched = 0
					break
				}
				matched = info.ContextLength
			}
		}
		if matched > 0 {
			return ContextWindowResolution{Tokens: matched, Source: "catalog-alias"}
		}
	}
	if learned != nil {
		if tokens := learned.Get(model); tokens > 0 {
			return ContextWindowResolution{Tokens: tokens, Source: "learned"}
		}
	}
	return ContextWindowResolution{}
}

// EstimateNextRequestTokens prices the complete prompt that is about to reach
// the provider, not only the persisted conversation. Tool definitions, the
// thin-tool catalog and the per-request stamp all consume the same context
// window. Keeping them in the trigger leaves the remaining 20% of the window
// available for generation instead of discovering the real size at the
// provider boundary.
