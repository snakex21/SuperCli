package llm

import (
	"strings"
	"sync/atomic"
)

// Thinking (chain-of-thought) control for LOCAL models that expose a
// soft switch in the prompt rather than an API parameter. Cloud
// reasoning models are steered by /reasoning (reasoning_effort); this
// is the orthogonal lever for the Qwen family, which honours the
// `/no_think` token appended to the prompt.
//
// Default is thinking ENABLED (thinkingDisabled == false): SuperCli's
// historical behaviour is unchanged until the user opts into saving
// tokens/latency with `/think off`.
var thinkingDisabled atomic.Bool

// SetThinkingEnabled sets the process-global thinking state. true keeps
// full chain-of-thought (default); false suppresses it where the model
// supports a prompt soft switch.
func SetThinkingEnabled(on bool) { thinkingDisabled.Store(!on) }

// ThinkingEnabled reports whether chain-of-thought is currently on.
func ThinkingEnabled() bool { return !thinkingDisabled.Load() }

// SupportsThinkingSoftSwitch reports whether model honours an in-prompt
// thinking toggle (currently the Qwen family via /think and /no_think).
func SupportsThinkingSoftSwitch(model string) bool {
	m := strings.ToLower(model)
	return strings.Contains(m, "qwen")
}

// ThinkingDirective returns the soft-switch token to append to the
// trailing prompt so a supporting model suppresses its chain-of-thought.
// Empty string when thinking is enabled or the model has no soft switch,
// so callers can append unconditionally. Kept append-only (end of the
// prompt) by callers so the cacheable prefix is not disturbed.
func ThinkingDirective(model string) string {
	if ThinkingEnabled() {
		return ""
	}
	if SupportsThinkingSoftSwitch(model) {
		return "/no_think"
	}
	return ""
}

// cachePromptDefault is the process-global cache_prompt override, set
// once at startup from config.toml `cache_prompt`. nil = unset (each
// provider auto-detects by host). A per-construction
// OpenAIConfig.CachePrompt still wins over this. Stored via a holder so
// atomic.Value can carry a nil *bool.
type cachePromptHolder struct{ v *bool }

var cachePromptDefault atomic.Value

// SetCachePromptDefault sets the process-global cache_prompt default.
// Pass nil to clear it (back to per-host auto-detection).
func SetCachePromptDefault(v *bool) { cachePromptDefault.Store(cachePromptHolder{v}) }

// CachePromptDefault returns the process-global default, or nil when
// unset (per-host auto-detection). The exported read of what
// SetCachePromptDefault installed.
func CachePromptDefault() *bool { return cachePromptDefaultVal() }

// cachePromptDefaultVal returns the current global default, or nil when
// unset. Consulted by NewOpenAI when no explicit CachePrompt is given.
func cachePromptDefaultVal() *bool {
	if h, ok := cachePromptDefault.Load().(cachePromptHolder); ok {
		return h.v
	}
	return nil
}
