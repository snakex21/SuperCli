// Reasoning-effort support for OpenAI-family models.
//
// OpenAI reasoning models accept a per-request effort level
// (chat completions: "reasoning_effort"; Responses API:
// "reasoning": {"effort": ...}). Supported values are
// model-dependent: none, minimal, low, medium, high, xhigh
// (xhigh only on the codex-max family; "none" from gpt-5.1 on).
//
// The active level is process-global (one active chat model at a
// time) and is set by the /reasoning slash command or the
// `reasoning_effort` key in config.toml. Providers read it at
// request-build time, and ONLY attach it for models known to
// support the parameter — unknown models never see it, so local
// or non-OpenAI endpoints cannot fail on an unexpected field.
package llm

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// reasoningEffortLevel holds the active level ("" = unset).
var reasoningEffortLevel atomic.Value

// ReasoningEffortLevels are the accepted /reasoning arguments.
var ReasoningEffortLevels = []string{"none", "minimal", "low", "medium", "high", "xhigh"}

// SetReasoningEffort sets the process-global effort level.
// Empty string clears it (provider default). Returns an error
// for unknown levels.
func SetReasoningEffort(level string) error {
	level = strings.ToLower(strings.TrimSpace(level))
	if level != "" && !isValidReasoningEffort(level) {
		return fmt.Errorf("unknown reasoning effort %q (want %s)",
			level, strings.Join(ReasoningEffortLevels, "|"))
	}
	reasoningEffortLevel.Store(level)
	return nil
}

// ReasoningEffort returns the active level ("" = unset).
func ReasoningEffort() string {
	if v, ok := reasoningEffortLevel.Load().(string); ok {
		return v
	}
	return ""
}

func isValidReasoningEffort(level string) bool {
	for _, l := range ReasoningEffortLevels {
		if l == level {
			return true
		}
	}
	return false
}

// SupportsXHighReasoningEffort reports whether the model accepts
// the "xhigh" effort level (codex-max family only).
func SupportsXHighReasoningEffort(model string) bool {
	m := strings.ToLower(model)
	if i := strings.LastIndexByte(m, '/'); i >= 0 {
		m = m[i+1:]
	}
	return strings.Contains(m, "codex-max")
}

// NextReasoningEffort returns the level that follows current in
// the Ctrl+R cycle: off → minimal → low → medium → high →
// (xhigh if allowed) → off. "" means off/provider default.
// Levels outside the cycle (e.g. "none" set via /reasoning)
// restart the cycle at off.
func NextReasoningEffort(current string, xhigh bool) string {
	order := []string{"", "minimal", "low", "medium", "high"}
	if xhigh {
		order = append(order, "xhigh")
	}
	for i, l := range order {
		if l == current {
			return order[(i+1)%len(order)]
		}
	}
	return ""
}

// SupportsReasoningEffort reports whether the model is known to
// accept the reasoning-effort parameter. Conservative allowlist
// by family: OpenAI o-series (o1/o3/o4...) and the gpt-5 family
// (including codex variants). Anything else — local models,
// gpt-4o, third-party ids — never gets the parameter.
func SupportsReasoningEffort(model string) bool {
	m := strings.ToLower(model)
	// Strip a provider prefix like "openai/gpt-5.5".
	if i := strings.LastIndexByte(m, '/'); i >= 0 {
		m = m[i+1:]
	}
	switch {
	case strings.HasPrefix(m, "gpt-5"):
		return true
	case strings.HasPrefix(m, "codex-") || m == "codex":
		return true
	case strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4"):
		return true
	}
	return false
}
