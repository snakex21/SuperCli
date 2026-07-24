package webgui

import (
	"path/filepath"
	"testing"
)

func TestWorkspaceFolderRejectsEscape(t *testing.T) {
	home := t.TempDir()
	inside, err := workspaceFolder(home, filepath.Join(home, ".supercli", "prompts"))
	if err != nil || inside == "" {
		t.Fatalf("inside=%q err=%v", inside, err)
	}
	if _, err := workspaceFolder(home, filepath.Join(home, "..", "outside")); err == nil {
		t.Fatal("outside path accepted")
	}
}
