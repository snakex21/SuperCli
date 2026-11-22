package session

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"supercli/internal/storage"
)

// A damaged sessions.db no longer blocks the GUI's start: it is kept as a
// copy and a fresh store opens (1.0.39).
func TestOpenStoreRecoversFromCorruptSessionsDB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.db")
	garbage := bytes.Repeat([]byte{0xde, 0xad, 0xbe, 0xef}, 4096)
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("damaged sessions.db stopped the start: %v", err)
	}
	defer store.Close()
	if _, err := store.Create(dir, "m", "after recovery"); err != nil {
		t.Fatalf("fresh store unusable: %v", err)
	}
	found := false
	for _, r := range storage.Recoveries() {
		if r.File == path {
			kept, _ := os.ReadFile(r.Backup)
			found = bytes.Equal(kept, garbage)
		}
	}
	if !found {
		t.Fatal("the damaged sessions.db was not kept as a copy")
	}
}
