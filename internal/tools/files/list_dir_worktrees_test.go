package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListTreeMarksWorktreesWithoutExpandingThem(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".claude/worktrees/agent/old.txt", ".claude/skills/SKILL.md", "src/current.go"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tool := NewListDir(root)
	r, err := tool.Execute(context.Background(), json.RawMessage("{\"depth\":4}"))
	if err != nil || r.Err != nil || !strings.Contains(r.Text, ".claude/worktrees/ (not expanded)") || strings.Contains(r.Text, "old.txt") || !strings.Contains(r.Text, "SKILL.md") {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = tool.Execute(context.Background(), json.RawMessage("{\"path\":\".claude/worktrees\",\"depth\":4}"))
	if err != nil || r.Err != nil || !strings.Contains(r.Text, "old.txt") {
		t.Fatalf("%+v %v", r, err)
	}
}
