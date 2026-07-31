package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An append-only diagnostic log on a machine nobody prunes must have a
// ceiling. Past the cap the current file moves to "<path>.1" and a
// fresh one starts, so the pair is bounded by twice the cap.
func TestLineWriter_RotatesPastCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool_errors.log")
	w, err := newLineWriter(path, 64)
	if err != nil {
		t.Fatalf("newLineWriter: %v", err)
	}
	line := []byte(strings.Repeat("a", 40) + "\n")
	for i := 0; i < 5; i++ {
		if _, err := w.Write(line); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	live, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat live: %v", err)
	}
	if live.Size() > 64 {
		t.Fatalf("live log exceeded the cap: %d bytes", live.Size())
	}
	rotated, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatalf("stat rotated: %v", err)
	}
	if rotated.Size() == 0 {
		t.Fatal("rotated file is empty; the old records were lost instead of moved")
	}
	// Exactly one rotation is kept, never a growing pile.
	if _, err := os.Stat(path + ".2"); !os.IsNotExist(err) {
		t.Fatalf("unexpected second rotation file: %v", err)
	}
}

func TestLineWriter_NoRotationWhenCapDisabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.log")
	w, err := newLineWriter(path, 0)
	if err != nil {
		t.Fatalf("newLineWriter: %v", err)
	}
	for i := 0; i < 20; i++ {
		if _, err := w.Write([]byte("line\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("rotated with the cap disabled: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := strings.Count(string(b), "line\n"); got != 20 {
		t.Fatalf("want 20 lines, got %d", got)
	}
}
