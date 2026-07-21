package prompt

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestUserInstructionsRoundTripAndActivePrompt(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("instrukcja ", 6000)
	got, err := SaveUserInstructions(dir, UserInstructionsState{
		Enabled:  true,
		ActiveID: "office",
		Presets: []UserInstructionPreset{
			{ID: "office", Name: "Praca biurowa", Content: long},
			{ID: "short", Name: "Krótko", Content: "Odpowiadaj krótko."},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Presets[0].Content) != len(long) {
		t.Fatal("long instructions were truncated")
	}
	prompt := ActiveUserInstructions(dir)
	if !strings.Contains(prompt, "Praca biurowa") || !strings.Contains(prompt, strings.TrimSpace(long)) {
		t.Fatal("active preset is missing from prompt")
	}
	info, err := os.Stat(UserInstructionsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("instruction file permissions = %v", info.Mode().Perm())
	}
}

func TestDisabledUserInstructionsCostNoPromptText(t *testing.T) {
	dir := t.TempDir()
	_, err := SaveUserInstructions(dir, UserInstructionsState{
		Enabled:  false,
		ActiveID: "one",
		Presets:  []UserInstructionPreset{{ID: "one", Name: "One", Content: "Never included"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := ActiveUserInstructions(dir); got != "" {
		t.Fatalf("disabled instructions = %q", got)
	}
}

func TestUserInstructionsStayLocalToTheirDataDirectory(t *testing.T) {
	superCliData := t.TempDir()
	nestCafeData := t.TempDir()
	_, err := SaveUserInstructions(superCliData, UserInstructionsState{
		Enabled:  true,
		ActiveID: "supercli",
		Presets:  []UserInstructionPreset{{ID: "supercli", Name: "SuperCli", Content: "Only standalone"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := ActiveUserInstructions(nestCafeData); got != "" {
		t.Fatalf("NestCafe data unexpectedly inherited SuperCli instructions: %q", got)
	}
	if got := ActiveUserInstructions(superCliData); !strings.Contains(got, "Only standalone") {
		t.Fatalf("SuperCli instructions missing: %q", got)
	}
}
