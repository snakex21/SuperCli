package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProfilePrecedenceAndCap(t *testing.T) {
	h := t.TempDir()
	d := filepath.Join(h, ".supercli", "prompts")
	if err := os.MkdirAll(d, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "qwen.md"), []byte("family"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := LoadProfile(h, "Qwen3-8B"); !strings.Contains(got, "family") {
		t.Fatalf("family profile=%q", got)
	}
	if err := os.WriteFile(filepath.Join(d, "qwen3-8b.md"), []byte(strings.Repeat("x", 5000)), 0644); err != nil {
		t.Fatal(err)
	}
	if got := LoadProfile(h, "Qwen3-8B"); len(got) > 4200 || !strings.Contains(got, "xxxx") {
		t.Fatalf("exact/cap profile length=%d", len(got))
	}
}

func TestLoadProfileAtFallsBackToPortableDataAndProjectWins(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(data, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "profiles", "qwen.md"), []byte("portable family"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadProfileAt(home, data, "Qwen3-8B"); !strings.Contains(got, "portable family") {
		t.Fatalf("portable profile=%q", got)
	}
	if err := os.MkdirAll(filepath.Join(home, ".supercli", "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".supercli", "prompts", "qwen.md"), []byte("project override"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadProfileAt(home, data, "Qwen3-8B"); !strings.Contains(got, "project override") {
		t.Fatalf("project precedence=%q", got)
	}
}
