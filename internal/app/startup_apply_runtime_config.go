package app

import (
	"log"
	"os"

	"supercli/internal/llm"
	"supercli/internal/storage/memory"
	"supercli/internal/system/config"
)

// applyRuntimeConfig loads flag overrides into config.Config, merges TOML
// and active-project preferences, restores reasoning/thinking, and applies
// draft/credit defaults when flags were left at zero/off.
// Mutates f in place for draft/credit overrides from TOML.
func applyRuntimeConfig(f *cliFlags, tomlCfg config.TomlConfig, tomlErr error, activeProject memory.Project, hasActiveProject bool) config.Config {
	// --echo short-circuits config.
	if f.Echo {
		f.Key = ""
		f.Provider = config.ProviderEcho
	}

	cfg, err := config.Load(config.FlagOverrides{
		Provider: f.Provider,
		APIKey:   f.Key,
		BaseURL:  f.BaseURL,
		Model:    f.Model,
		Debug:    boolPtr(f.Debug),
	})
	if err != nil {
		fatal("load config", err)
	}
	// F29: apply TOML defaults for fields not set by env/flags.
	if tomlErr == nil {
		config.ApplyTomlToConfig(&cfg, tomlCfg)
	}
	// Active project's preferred model/provider. More specific than the
	// global TOML default, so it wins over it — but explicit --model /
	// --provider flags and the corresponding env vars still win over the
	// project. When a project names a provider, resolve its base URL + API
	// key from the providers list (same fields a runtime /model swap uses)
	// so the model actually reaches the right endpoint.
	if hasActiveProject {
		if activeProject.Provider != "" && f.Provider == "" && os.Getenv("SUPERCLI_LLM_PROVIDER") == "" {
			for _, pc := range tomlCfg.Providers {
				if pc.Name == activeProject.Provider {
					cfg.Provider = pc.Type
					cfg.BaseURL = pc.BaseURL
					cfg.APIKey = pc.APIKey
					break
				}
			}
		}
		if activeProject.Model != "" && f.Model == "" && os.Getenv("SUPERCLI_LLM_MODEL") == "" {
			cfg.Model = activeProject.Model
		}
		if err := cfg.Normalize(); err != nil {
			log.Printf("project %q: normalize config: %v (ignored)", activeProject.Name, err)
		}
	}
	// Reasoning effort (OpenAI reasoning models): restore the
	// persisted level; /reasoning changes it at runtime.
	if tomlCfg.ReasoningEffort != "" {
		if err := llm.SetReasoningEffort(tomlCfg.ReasoningEffort); err != nil {
			log.Printf("config: reasoning_effort: %v (ignored)", err)
		}
	}
	// Thinking soft switch (local Qwen /no_think): restore the
	// persisted state; /think changes it at runtime. Default (nil) is
	// thinking ON — unchanged from historical behaviour.
	if tomlCfg.Thinking != nil {
		llm.SetThinkingEnabled(*tomlCfg.Thinking)
	}
	// Apply draft/credit overrides from TOML if not set by flags.
	if f.DraftMode == "off" && tomlCfg.DraftMode != "" {
		f.DraftMode = tomlCfg.DraftMode
	}
	if f.DraftModel == "" && tomlCfg.DraftModel != "" {
		f.DraftModel = tomlCfg.DraftModel
	}
	if f.MaxSession == 0 && tomlCfg.MaxCreditsPerSession > 0 {
		f.MaxSession = tomlCfg.MaxCreditsPerSession
	}
	if f.MaxDay == 0 && tomlCfg.MaxCreditsPerDay > 0 {
		f.MaxDay = tomlCfg.MaxCreditsPerDay
	}
	if cfg.Debug {
		log.Printf("config: %+v", cfg.Sanitized())
	}
	return cfg
}
