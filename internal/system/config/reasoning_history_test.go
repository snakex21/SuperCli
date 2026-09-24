package config

import (
	"path/filepath"
	"supercli/internal/llm"
	"testing"
)

func TestDiscardPreviousReasoningConfigDefaultMergeAndRestart(t *testing.T) {
	old := llm.DiscardPreviousReasoning()
	t.Cleanup(func() { llm.SetDiscardPreviousReasoning(old) })
	on, off := true, false
	cfg := TomlConfig{DiscardPreviousReasoning: &on}
	mergeToml(&cfg, TomlConfig{DiscardPreviousReasoning: &off})
	if cfg.DiscardPreviousReasoning == nil || *cfg.DiscardPreviousReasoning {
		t.Fatal("explicit project false lost")
	}
	mergeToml(&cfg, TomlConfig{})
	if cfg.DiscardPreviousReasoning == nil || *cfg.DiscardPreviousReasoning {
		t.Fatal("unset project overrode global setting")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg.DiscardPreviousReasoning = &on
	if err := SaveToml(path, cfg); err != nil {
		t.Fatal(err)
	}
	restored, err := LoadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	ApplyLLMGlobals(restored, nil)
	if !llm.DiscardPreviousReasoning() {
		t.Fatal("saved option not restored")
	}
	ApplyLLMGlobals(TomlConfig{}, nil)
	if llm.DiscardPreviousReasoning() {
		t.Fatal("default should preserve reasoning")
	}
}
