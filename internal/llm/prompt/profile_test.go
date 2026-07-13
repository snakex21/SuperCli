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
