package manifest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A new file must change the tree hash.
func TestCompute_NewFileChangesHash(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.go"), "package a")
	h1, err := Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	write(t, filepath.Join(dir, "b.go"), "package a")
	h2, err := Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if h1 == h2 {
		t.Error("adding a file did not change the hash")
	}
}

// A size change and an mtime change must each move the hash;
// an untouched tree must hash identically.
func TestCompute_SizeAndMtime(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	write(t, f, "one")
	h1, _ := Compute(dir)
	h1b, _ := Compute(dir)
	if h1 != h1b {
		t.Error("hash not stable on an untouched tree")
	}
	// Size change.
	write(t, f, "one two")
	h2, _ := Compute(dir)
	if h2 == h1 {
		t.Error("size change did not move the hash")
	}
	// mtime-only change (same content/size).
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(f, future, future); err != nil {
		t.Fatal(err)
	}
	h3, _ := Compute(dir)
	if h3 == h2 {
		t.Error("mtime change did not move the hash")
	}
}

// Ignored directories (.git etc.) must not contribute — the shared
// tools/search ignore set is in effect.
func TestCompute_RespectsIgnoreSet(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.go"), "package a")
	h1, _ := Compute(dir)
	write(t, filepath.Join(dir, ".git", "objects", "x"), "blob")
	write(t, filepath.Join(dir, "node_modules", "m", "i.js"), "x")
	h2, _ := Compute(dir)
	if h1 != h2 {
		t.Error(".git/node_modules content changed the hash — ignore set not applied")
	}
}

// extraIgnore excludes SuperCli's own state (e.g. a portable data
// dir inside the project root) so saving the manifest itself can
// never invalidate the manifest.
func TestCompute_ExtraIgnore(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.go"), "package a")
	data := filepath.Join(dir, "supercli-data")
	h1, _ := Compute(dir, data)
	write(t, filepath.Join(data, "projects", "p", "noop", "m.json"), "{}")
	h2, _ := Compute(dir, data)
	if h1 != h2 {
		t.Error("state under extraIgnore changed the hash")
	}
}

func TestOpKey(t *testing.T) {
	if OpKey("run tests") != OpKey("run tests") {
		t.Error("OpKey not stable")
	}
	if OpKey("run tests") == OpKey("run tests!") {
		t.Error("different prompts share an OpKey")
	}
	if len(OpKey("x")) != 16 {
		t.Errorf("OpKey length = %d, want 16", len(OpKey("x")))
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deep", "m.json")
	in := Record{Hash: "abc", UpdatedAt: time.Now().UTC().Truncate(time.Second), Operation: "op"}
	if err := Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Hash != in.Hash || !out.UpdatedAt.Equal(in.UpdatedAt) || out.Operation != in.Operation {
		t.Errorf("roundtrip mismatch: %+v != %+v", out, in)
	}
	// Missing file is an error (callers fail open).
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("Load of a missing file must error")
	}
	// Corrupt file is an error too.
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte("{not json"), 0o644)
	if _, err := Load(bad); err == nil {
		t.Error("Load of a corrupt file must error")
	}
}
