package app

import (
	"testing"

	"supercli/internal/llm/prompt"
)

func TestMainLoopInitialInstructionsIncludeSelectedPreset(t *testing.T) {
	dir := t.TempDir()
	if _, err := prompt.SaveUserInstructions(dir, prompt.UserInstructionsState{
		Enabled: true, ActiveID: "initial",
		Presets: []prompt.UserInstructionPreset{{ID: "initial", Name: "Initial", Content: "Already selected at startup."}},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := buildMainLoopConfig(loopAssembly{dataDir: dir})
	if cfg.UserInstructions != prompt.ActiveUserInstructions(dir) || cfg.UserInstructions == "" {
		t.Fatal("initial context accounting did not include selected instructions")
	}
}
