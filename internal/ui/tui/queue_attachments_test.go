package tui

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/storage/session"
)

// Web GUI rows can carry attachments; the TUI queue must send them instead of
// silently dropping them, and must leave the composer's own selection alone.
func TestQueuedTaskSendsStoredAttachments(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	store, err := session.OpenStore(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	imagePath := filepath.Join(home, "queued.png")
	file, err := os.Create(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(file, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	file.Close()
	row, err := store.EnqueueTaskWithAttachments(context.Background(), home, "", "queued with image", []string{imagePath})
	if err != nil {
		t.Fatal(err)
	}
	a := &attachmentTestAgent{}
	m := New(Options{Home: home, DataDir: data, SessionStore: store, Agent: a, NoColor: true})
	next, _ := m.openQueueMenu()
	m = next.(Model)
	if !strings.Contains(m.renderQueueMenu(), "[+1]") {
		t.Fatalf("queue menu does not show the attachment count:\n%s", m.renderQueueMenu())
	}
	m.pendingAttachments = []string{"composer-only.png"}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("queued task did not start")
	}
	started, ok := cmd().(runStartMsg)
	if !ok || started.err != nil {
		t.Fatalf("start=%+v", started)
	}
	if a.runs != 1 || len(a.sentImages) != 1 || !strings.Contains(a.sentAddon, "queued.png") {
		t.Fatalf("queued attachments not sent: %+v", a)
	}
	if !reflect.DeepEqual(m.pendingAttachments, []string{"composer-only.png"}) {
		t.Fatalf("composer selection changed: %v", m.pendingAttachments)
	}
	rows, err := store.ListQueuedTasks(context.Background(), home)
	if err != nil || len(rows) != 0 {
		t.Fatalf("accepted row %s not removed: %+v / %v", row.ID, rows, err)
	}
}
