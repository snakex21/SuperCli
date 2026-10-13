package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectInstructionsPrefersAgentsMD(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "CLAUDE.md"), []byte("claude rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadProjectInstructions(d); !strings.Contains(got, "claude rules") || !strings.Contains(got, "(CLAUDE.md)") {
		t.Fatalf("CLAUDE.md fallback=%q", got)
	}
	if err := os.WriteFile(filepath.Join(d, "AGENTS.md"), []byte("agents rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadProjectInstructions(d)
	if !strings.Contains(got, "agents rules") || strings.Contains(got, "claude rules") {
		t.Fatalf("AGENTS.md precedence=%q", got)
	}
	if LoadProjectInstructions(d) != got {
		t.Fatal("block is not byte-stable")
	}
}

func TestProjectInstructionsWalksUpToGitRoot(t *testing.T) {
	outer := t.TempDir()
	repo := filepath.Join(outer, "repo")
	sub := filepath.Join(repo, "pkg", "x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file above the git root must never leak into the repo.
	if err := os.WriteFile(filepath.Join(outer, "AGENTS.md"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := LoadProjectInstructions(sub); got != "" {
		t.Fatalf("leaked instructions from above git root: %q", got)
	}
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("repo rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadProjectInstructions(sub); !strings.Contains(got, "repo rules") {
		t.Fatalf("repo root instructions=%q", got)
	}
}

func TestProjectInstructionsNoGitChecksOnlyWorkspace(t *testing.T) {
	outer := t.TempDir()
	sub := filepath.Join(outer, "work")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outer, "AGENTS.md"), []byte("stray"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadProjectInstructions(sub); got != "" {
		t.Fatalf("stray parent instructions leaked: %q", got)
	}
	if got := LoadProjectInstructions(outer); !strings.Contains(got, "stray") {
		t.Fatalf("workspace instructions=%q", got)
	}
}

func TestProjectInstructionsCapped(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "AGENTS.md"), []byte(strings.Repeat("ż", maxProjectInstructionBytes)), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadProjectInstructions(d)
	if len(got) > maxProjectInstructionBytes+1024 || !strings.Contains(got, "[truncated;") {
		t.Fatalf("cap not applied, len=%d", len(got))
	}
}
