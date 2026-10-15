package webgui

import (
	"strings"
	"testing"

	"supercli/internal/tools/sandbox"
)

func TestNestCafeSystemPromptUsesOfficeProfile(t *testing.T) {
	previous := sandbox.IsUnsandboxed()
	sandbox.SetUnsandboxed(true)
	t.Cleanup(func() { sandbox.SetUnsandboxed(previous) })
	home := t.TempDir()
	system := webAgentSystemPrompt(home, t.TempDir(), "test-model", true, false, false, nil, "nestcafe")
	// The per-project workspace note rides the request tail, never the stable
	// system prefix, so switching projects keeps the cached prefix intact.
	if strings.Contains(system, home) {
		t.Fatal("workspace path leaked into the stable system prompt")
	}
	prompt := system + "\n\n" + webWorkspaceNote(home, true)
	for _, expected := range []string{
		"NestCafe office mode",
		"general desktop and office assistant",
		"Full access to ordinary user files is ON",
		"do not claim that the active workspace prevents access",
		"Word, Excel, PDF",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("office prompt missing %q:\n%s", expected, prompt)
		}
	}
	if strings.Contains(prompt, "Reference code as file_path:line") {
		t.Fatal("NestCafe office prompt unexpectedly includes the code-oriented extended profile")
	}
}

func TestNestCafeProfileDisablesRepositoryPreflight(t *testing.T) {
	engine := &Engine{dataDir: t.TempDir(), appProfile: "nestcafe"}
	if block, tokens := engine.preflightBlockAt(t.TempDir()); block != "" || tokens != 0 {
		t.Fatalf("NestCafe preflight = %q, %d tokens; want disabled", block, tokens)
	}
}
