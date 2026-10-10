package checkpoint

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRefusesMalformedCheckpointMetadataWithoutReplacingIt(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(`[{"id":"retained","files":["a.txt"]},`)
	if err := os.WriteFile(m.meta, original, 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(home, data)
	if err == nil || reopened != nil || !strings.Contains(err.Error(), "read checkpoint records") {
		t.Fatalf("Open accepted partial metadata: manager=%v err=%v", reopened, err)
	}
	actual, err := os.ReadFile(m.meta)
	if err != nil || !bytes.Equal(actual, original) {
		t.Fatalf("metadata was replaced: bytes=%q err=%v", actual, err)
	}
	if _, err := os.Stat(m.meta + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("unexpected metadata write: %v", err)
	}
}

func TestOpenRefusesUnreadableCheckpointMetadata(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(m.meta, 0700); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(home, data)
	if err == nil || reopened != nil {
		t.Fatalf("Open ignored read failure: manager=%v err=%v", reopened, err)
	}
	info, err := os.Stat(m.meta)
	if err != nil || !info.IsDir() {
		t.Fatalf("metadata directory changed: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(m.meta), "turns.json.tmp")); !os.IsNotExist(err) {
		t.Fatalf("unexpected metadata write: %v", err)
	}
}
