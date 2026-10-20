package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"supercli/internal/llm/prompt"
)

func TestUserInstructionsForRunReflectsSharedPresetsWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	refresh := userInstructionsForRun(dir)
	ctx := context.Background()
	if got, err := refresh(ctx); err != nil || got != "" {
		t.Fatalf("no instructions: %q, %v", got, err)
	}
	long := strings.Repeat("Własne instrukcje\nDrugi wiersz.\n", 2000)
	state := prompt.UserInstructionsState{
		Enabled: true, ActiveID: "first",
		Presets: []prompt.UserInstructionPreset{
			{ID: "first", Name: "Pierwszy", Content: long},
			{ID: "second", Name: "Drugi", Content: "Only the second preset."},
		},
	}
	if _, err := prompt.SaveUserInstructions(dir, state); err != nil {
		t.Fatal(err)
	}
	first, err := refresh(ctx)
	if err != nil || first != prompt.ActiveUserInstructions(dir) || !strings.Contains(first, strings.TrimSpace(long)) {
		t.Fatalf("shared GUI format or long content was not loaded: %v", err)
	}
	if again, err := refresh(ctx); err != nil || again != first {
		t.Fatal("unchanged preset changed cacheable prompt bytes")
	}
	state.ActiveID = "second"
	if _, err := prompt.SaveUserInstructions(dir, state); err != nil {
		t.Fatal(err)
	}
	second, err := refresh(ctx)
	if err != nil || !strings.Contains(second, "Only the second preset.") || strings.Contains(second, "Własne instrukcje") {
		t.Fatalf("preset selection did not replace the old content: %v", err)
	}
	state.Presets[1].Content = "Edited in TUI."
	if _, err := prompt.SaveUserInstructions(dir, state); err != nil {
		t.Fatal(err)
	}
	if got, err := refresh(ctx); err != nil || !strings.Contains(got, "Edited in TUI.") || strings.Contains(got, "Only the second preset.") {
		t.Fatal("editing the active preset did not replace its prompt")
	}
	state.Enabled = false
	if _, err := prompt.SaveUserInstructions(dir, state); err != nil {
		t.Fatal(err)
	}
	if got, err := refresh(ctx); err != nil || got != "" {
		t.Fatalf("disabled preset retained instructions: %q, %v", got, err)
	}
	state.Enabled = true
	if _, err := prompt.SaveUserInstructions(dir, state); err != nil {
		t.Fatal(err)
	}
	if got, err := refresh(ctx); err != nil || !strings.Contains(got, "Edited in TUI.") {
		t.Fatal("re-enabling did not restore the selected preset")
	}
	other := userInstructionsForRun(t.TempDir())
	if got, err := other(ctx); err != nil || got != "" {
		t.Fatal("instructions leaked into another data directory")
	}
}

func TestUserInstructionsForRunReportsCorruptionAndCancellation(t *testing.T) {
	dir := t.TempDir()
	refresh := userInstructionsForRun(dir)
	if err := os.WriteFile(prompt.UserInstructionsPath(dir), []byte(`{"broken"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := refresh(context.Background()); err == nil {
		t.Fatal("corrupt shared state silently dropped enabled instructions")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := refresh(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err := prompt.SaveUserInstructions(dir, prompt.UserInstructionsState{}); err != nil {
		t.Fatal(err)
	}
	if got, err := refresh(context.Background()); err != nil || got != "" {
		t.Fatal("refresh did not recover after configuration repair")
	}
}

func TestMainLoopConfigReloadsInstructionsFromEffectiveDataDirectory(t *testing.T) {
	dir := t.TempDir()
	original := supercliUserInstructions
	supercliUserInstructions = "stale startup instructions"
	t.Cleanup(func() { supercliUserInstructions = original })
	cfg := buildMainLoopConfig(loopAssembly{dataDir: dir})
	if cfg.UserInstructionsForRun == nil || strings.Contains(cfg.System, "stale startup instructions") {
		t.Fatal("main loop did not wire reload or retained the startup preset")
	}
	if _, err := prompt.SaveUserInstructions(dir, prompt.UserInstructionsState{
		Enabled: true, ActiveID: "current",
		Presets: []prompt.UserInstructionPreset{{ID: "current", Name: "Current", Content: "fresh current instructions"}},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := cfg.UserInstructionsForRun(context.Background())
	if err != nil || !strings.Contains(got, "fresh current instructions") || strings.Contains(got, "stale startup instructions") {
		t.Fatalf("effective data directory not loaded: %v", err)
	}
}
