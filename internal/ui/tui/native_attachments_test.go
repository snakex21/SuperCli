package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"supercli/internal/ui/attachments"
)

func TestNativeAttachmentPickerPreservesDraftAndCancel(t *testing.T) {
	dir := t.TempDir()
	m := New(Options{Home: dir, NoColor: true, Language: "pl"})
	m.input.SetValue("opisz zdjęcie")
	file := filepath.Join(dir, "żółć.png")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	next, cmd := m.beginNativeAttachments(func(gotDir, lang string) ([]string, error) {
		if gotDir != dir || lang != "pl" {
			t.Fatalf("picker args %q %q", gotDir, lang)
		}
		return []string{file}, nil
	})
	m = next.(Model)
	// The open modal cannot accidentally submit the underlying draft or spawn another dialog.
	held, unexpected := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if held.(Model).input.Value() != "opisz zdjęcie" || unexpected != nil {
		t.Fatal("modal submitted draft")
	}
	_, duplicate := m.beginNativeAttachments(nil)
	if duplicate != nil {
		t.Fatal("duplicate picker")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.attachmentPickerOpen || m.busy || m.input.Value() != "opisz zdjęcie" || !reflect.DeepEqual(m.pendingAttachments, []string{file}) {
		t.Fatal("selection changed draft/state")
	}
	next, cmd = m.beginNativeAttachments(func(string, string) ([]string, error) { return nil, nil })
	next, _ = next.(Model).Update(cmd())
	m = next.(Model)
	if m.input.Value() != "opisz zdjęcie" || !reflect.DeepEqual(m.pendingAttachments, []string{file}) || m.statusOverride != "" {
		t.Fatal("cancel lost selection/draft")
	}
}

func TestNativePickerFailureFallsBackToBrowser(t *testing.T) {
	m := New(Options{Home: t.TempDir(), NoColor: true})
	m.input.SetValue("keep")
	next, _ := m.applyNativeAttachments(nativeAttachmentsMsg{err: errors.New("dialog failed")})
	m = next.(Model)
	if m.mode != modeMenu || m.menu.kind != menuAttachments || m.input.Value() != "keep" || !strings.Contains(m.menu.formErr, "dialog failed") {
		t.Fatal("no useful fallback")
	}
}

func TestAttachmentSelectionDeduplicatesAndRejectsWholeInvalidBatch(t *testing.T) {
	dir := t.TempDir()
	paths := make([]string, attachments.MaxFiles+1)
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("zdjęcie %d.png", i))
		if err := os.WriteFile(paths[i], []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := New(Options{Home: dir, NoColor: true})
	if n, err := m.addAttachmentPaths(paths[:1]); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if n, err := m.addAttachmentPaths(paths[:1]); err != nil || n != 0 {
		t.Fatalf("repeated paste: %d %v", n, err)
	}
	for _, invalid := range [][]string{{paths[1], dir}, {paths[1], filepath.Join(dir, "missing.png")}, paths, {"relative.png"}} {
		if _, err := m.addAttachmentPaths(invalid); err == nil {
			t.Fatalf("accepted %#v", invalid)
		}
		if !reflect.DeepEqual(m.pendingAttachments, paths[:1]) {
			t.Fatal("partial selection after failure")
		}
	}
	if filepath.Separator == '\\' {
		if n, err := m.addAttachmentPaths([]string{strings.ToUpper(paths[0])}); err != nil || n != 0 {
			t.Fatalf("case duplicate: %d %v", n, err)
		}
	}
	if n, err := m.addAttachmentPaths(paths[:attachments.MaxFiles]); err != nil || n != attachments.MaxFiles-1 {
		t.Fatalf("valid max: %d %v", n, err)
	}
}

func TestAttachmentSelectionChecksSizeBeforeSend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(attachments.MaxFileBytes + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	m := New(Options{Home: dir})
	if _, err := m.addAttachmentPaths([]string{path}); err == nil || len(m.pendingAttachments) != 0 {
		t.Fatal("accepted oversized file")
	}
}

func TestHeaderDoesNotExposeObsoleteTier(t *testing.T) {
	for _, tier := range []string{"small", "big"} {
		m := New(Options{Tier: tier, NoColor: true})
		m.width = 120
		if strings.Contains(ansi.Strip(m.renderHeader()), tier) {
			t.Fatalf("tier %q still shown", tier)
		}
	}
}
