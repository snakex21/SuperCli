package app

import (
	"os"
	"path/filepath"
	"testing"

	"supercli/internal/system/manifest"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The knob is tri-state with default OFF: only an explicit true
// enables the gate.
func TestResolveNoopGate(t *testing.T) {
	if resolveNoopGate(nil) {
		t.Error("nil (default) must be OFF")
	}
	on, off := true, false
	if !resolveNoopGate(&on) {
		t.Error("explicit true must be ON")
	}
	if resolveNoopGate(&off) {
		t.Error("explicit false must be OFF")
	}
}

// Identical tree + saved manifest for the same prompt = skip.
func TestNoopGate_IdenticalTreeSkips(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(home, "a.go"), "package a")

	noopGateSave(data, home, "run the linter")
	rec, skip := noopGateSkip(data, home, "run the linter")
	if !skip {
		t.Fatal("unchanged tree with a saved manifest must skip")
	}
	if rec.Hash == "" || rec.UpdatedAt.IsZero() {
		t.Errorf("skip record incomplete: %+v", rec)
	}
}

// No prior manifest = run normally (fail open).
func TestNoopGate_NoManifestRuns(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(home, "a.go"), "package a")
	if _, skip := noopGateSkip(data, home, "never ran before"); skip {
		t.Error("missing manifest must NOT skip")
	}
}

// A changed tree = run normally.
func TestNoopGate_ChangedTreeRuns(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(home, "a.go"), "package a")
	noopGateSave(data, home, "task")
	writeTestFile(t, filepath.Join(home, "b.go"), "package a")
	if _, skip := noopGateSkip(data, home, "task"); skip {
		t.Error("changed tree must NOT skip")
	}
}

// A different prompt = independent manifest = run normally.
func TestNoopGate_DifferentPromptRuns(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(home, "a.go"), "package a")
	noopGateSave(data, home, "task A")
	if _, skip := noopGateSkip(data, home, "task B"); skip {
		t.Error("a different prompt must NOT reuse task A's manifest")
	}
}

// A broken (corrupt) manifest = IO/parse error = run normally.
func TestNoopGate_CorruptManifestRuns(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(home, "a.go"), "package a")
	writeTestFile(t, noopManifestPath(data, home, "task"), "{corrupt")
	if _, skip := noopGateSkip(data, home, "task"); skip {
		t.Error("corrupt manifest must NOT skip")
	}
}

// State written under the data dir (the manifest itself, session dbs)
// must not invalidate the fingerprint when the data dir lives inside
// the project root (portable mode).
func TestNoopGate_PortableDataDirInsideRoot(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "supercli-data")
	writeTestFile(t, filepath.Join(home, "a.go"), "package a")

	noopGateSave(data, home, "task")
	// Simulate more state churn under the data dir.
	writeTestFile(t, filepath.Join(data, "logs", "x.log"), "line")
	if _, skip := noopGateSkip(data, home, "task"); !skip {
		t.Error("data-dir churn inside the project root must not break the gate")
	}
}

// Sanity: the saved record survives a roundtrip through the real path
// layout ("<data>/projects/<key>/noop/<opkey>.json").
func TestNoopGate_ManifestPathShape(t *testing.T) {
	p := noopManifestPath(`C:\data`, `C:\proj`, "prompt")
	if filepath.Ext(p) != ".json" {
		t.Errorf("manifest path should be a .json file: %s", p)
	}
	if filepath.Base(filepath.Dir(p)) != "noop" {
		t.Errorf("manifest should live in a noop/ dir: %s", p)
	}
	if _, err := manifest.Load(p); err == nil {
		t.Error("loading a nonexistent manifest must error")
	}
}
