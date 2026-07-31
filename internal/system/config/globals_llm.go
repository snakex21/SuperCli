package config

import (
	"log"

	"supercli/internal/llm"
)

// ApplyLLMGlobals installs every process-global LLM setting that
// config.toml owns. It exists because those settings are process-wide
// state, not per-provider arguments: each start path (TUI, batch, web
// GUI, any future binary) must install them, and every path that
// forgot one silently ran with a different default than the user
// configured — cache_prompt was missing from the web GUI and batch,
// reasoning_effort and thinking from batch, thinking from the web GUI.
// One function called by all of them replaces four drifting copies.
//
// Call ONCE per process, BEFORE the first provider is built:
// NewOpenAI reads the cache_prompt and sampling defaults at
// construction time, so a provider built earlier keeps the built-in
// auto-detected values.
//
// envTemperature is config.Config.Temperature (the
// SUPERCLI_LLM_TEMPERATURE override, nil when unset); it outranks the
// `[sampling]` table.
//
// A zero TomlConfig is a valid argument and leaves every knob at its
// built-in default: cache_prompt nil (per-host auto-detection),
// sampling all-nil (nothing serialized into requests), reasoning
// effort untouched, thinking ON.
//
// (internal/llm imports no other internal package, so depending on it
// from here cannot introduce an import cycle — see sampling_llm.go.)
func ApplyLLMGlobals(t TomlConfig, envTemperature *float64) {
	// cache_prompt: nil = per-host auto-detection (local backends get
	// the llama.cpp KV-cache hint, cloud endpoints never do). A
	// non-nil value forces the hint in either direction.
	llm.SetCachePromptDefault(t.CachePrompt)

	// Sampling pass-through: `[sampling]` plus the
	// SUPERCLI_LLM_TEMPERATURE override. Nothing configured leaves
	// every field nil, and nil fields are never serialized, so a bare
	// config still sends a bare request.
	llm.SetSamplingDefault(t.Sampling.Resolve(envTemperature))

	// Reasoning effort (OpenAI reasoning models): restore the
	// persisted level; /reasoning changes it at runtime.
	if t.ReasoningEffort != "" {
		if err := llm.SetReasoningEffort(t.ReasoningEffort); err != nil {
			log.Printf("config: reasoning_effort: %v (ignored)", err)
		}
	}

	// Thinking soft switch (local Qwen /no_think): restore the
	// persisted state; /think changes it at runtime. Default (nil) is
	// thinking ON.
	if t.Thinking != nil {
		llm.SetThinkingEnabled(*t.Thinking)
	}
}
