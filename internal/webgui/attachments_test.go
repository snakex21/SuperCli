package webgui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools/sandbox"
)

func TestBuildAttachmentAddonUsesWorkspaceRelativePaths(t *testing.T) {
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(false) })
	home := t.TempDir()
	path := filepath.Join(home, "raport.docx")
	if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	addon, err := buildAttachmentAddon(home, []string{path, path})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(addon, "raport.docx") != 1 {
		t.Fatalf("attachment was not deduplicated: %q", addon)
	}
	if strings.Contains(addon, home) {
		t.Fatalf("workspace path was not shortened: %q", addon)
	}
}

func TestBuildAttachmentAddonRejectsOutsideWorkspace(t *testing.T) {
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(false) })
	home := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.pdf")
	if err := os.WriteFile(outside, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := buildAttachmentAddon(home, []string{outside}); err == nil {
		t.Fatal("outside attachment was accepted")
	}
}

func TestParsePickerPaths(t *testing.T) {
	got, err := parsePickerPaths([]byte("\xEF\xBB\xBF[\"C:\\\\docs\\\\a.docx\",\"C:\\\\docs\\\\b.pdf\"]"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1] != `C:\docs\b.pdf` {
		t.Fatalf("paths = %#v", got)
	}
}
