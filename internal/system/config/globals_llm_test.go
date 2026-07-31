package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestApplyLLMGlobalsInstallsConfiguredValues(t *testing.T) {
	t.Cleanup(func() {
		ApplyLLMGlobals(TomlConfig{}, nil)
		llm.SetThinkingEnabled(true)
		_ = llm.SetReasoningEffort("")
	})

	off := false
	temp := 0.25
	ApplyLLMGlobals(TomlConfig{
		CachePrompt:     &off,
		Thinking:        &off,
		ReasoningEffort: "low",
		Sampling:        SamplingConf{TopP: f64(0.9)},
	}, &temp)

	if llm.ThinkingEnabled() {
		t.Error("thinking = false in config.toml was not applied")
	}
	if got := llm.ReasoningEffort(); got != "low" {
		t.Errorf("reasoning_effort = %q, want %q", got, "low")
	}
	// cache_prompt and [sampling] are read at provider construction;
	// llm's own tests cover how a provider consumes the globals. Here
	// it is enough that ApplyLLMGlobals installed them without
	// dropping the SUPERCLI_LLM_TEMPERATURE override.
	if got := llm.SamplingDefault(); got.Temperature == nil || *got.Temperature != temp {
		t.Errorf("sampling temperature = %v, want the env override %v", got.Temperature, temp)
	} else if got.TopP == nil || *got.TopP != 0.9 {
		t.Errorf("sampling top_p = %v, want 0.9 from [sampling]", got.TopP)
	}
	if got := llm.CachePromptDefault(); got == nil || *got {
		t.Errorf("cache_prompt default = %v, want an explicit false", got)
	}
}

func TestApplyLLMGlobalsZeroConfigClearsCachePrompt(t *testing.T) {
	on := true
	ApplyLLMGlobals(TomlConfig{CachePrompt: &on}, nil)
	// A config without cache_prompt must return to per-host
	// auto-detection rather than inherit a stale global.
	ApplyLLMGlobals(TomlConfig{}, nil)
	if got := llm.CachePromptDefault(); got != nil {
		t.Errorf("cache_prompt default = %v, want nil (auto-detection)", *got)
	}
}

// TestEveryStartPathInstallsLLMGlobals guards the bug class this
// function exists for: a start path that builds providers but forgets
// the process-global settings silently runs with different defaults
// than the user configured. Each entry is a process entry point that
// builds an LLM provider from config.toml.
func TestEveryStartPathInstallsLLMGlobals(t *testing.T) {
	startPaths := []string{
		filepath.Join("..", "..", "app", "startup_apply_runtime_config.go"), // TUI
		filepath.Join("..", "..", "app", "cmd_batch.go"),                    // --batch
		filepath.Join("..", "..", "..", "cmd", "supercli-web", "main.go"),   // web GUI
	}
	for _, path := range startPaths {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(src), "ApplyLLMGlobals(") {
			t.Errorf("%s builds providers but never calls config.ApplyLLMGlobals: "+
				"cache_prompt/[sampling]/reasoning_effort/thinking from config.toml "+
				"would be ignored on this start path", path)
		}
	}
}

func f64(v float64) *float64 { return &v }
