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

func TestStagePickedAttachmentsCopiesOutsideFileIntoWorkspace(t *testing.T) {
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(false) })
	home := t.TempDir()
	outside := filepath.Join(t.TempDir(), "zdjęcie testowe.png")
	if err := os.WriteFile(outside, []byte("png data"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := stagePickedAttachments(home, []string{outside})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !sandbox.IsUnder(home, got[0]) {
		t.Fatalf("staged paths = %#v", got)
	}
	if !strings.Contains(got[0], filepath.Join(".supercli", "attachments")) {
		t.Fatalf("attachment was not staged in the hidden project directory: %q", got[0])
	}
	data, err := os.ReadFile(got[0])
	if err != nil || string(data) != "png data" {
		t.Fatalf("staged data = %q err=%v", data, err)
	}
	if _, err := buildAttachmentAddon(home, got); err != nil {
		t.Fatalf("staged attachment was not accepted by chat: %v", err)
	}
}

func TestStagePickedAttachmentsKeepsOriginalPathInOfficeMode(t *testing.T) {
	sandbox.SetUnsandboxed(true)
	t.Cleanup(func() { sandbox.SetUnsandboxed(false) })
	home := t.TempDir()
	outside := filepath.Join(t.TempDir(), "office-report.docx")
	if err := os.WriteFile(outside, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := stagePickedAttachments(home, []string{outside})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != outside {
		t.Fatalf("picked paths = %#v, want original %q", got, outside)
	}
	addon, err := buildAttachmentAddon(home, got)
	if err != nil {
		t.Fatalf("original office attachment was not accepted: %v", err)
	}
	if !strings.Contains(addon, "office-report.docx") || strings.Contains(addon, ".supercli") {
		t.Fatalf("attachment prompt does not preserve original path: %q", addon)
	}
}
