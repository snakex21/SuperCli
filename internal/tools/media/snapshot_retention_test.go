package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type retentionFixture struct {
	t        *testing.T
	base     string
	snapDir  string
	mediaDir string
	now      time.Time
}

func newRetentionFixture(t *testing.T) *retentionFixture {
	t.Helper()
	base := t.TempDir()
	f := &retentionFixture{t: t, base: base,
		snapDir:  filepath.Join(base, ".supercli", "snapshots"),
		mediaDir: filepath.Join(base, snapshotSessionMediaDir, "0123456789abcdef0123456789abcdef"),
		now:      time.Now()}
	for _, dir := range []string{f.snapDir, f.mediaDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// snapshot writes a snapshot file aged by age; copied also publishes the same
// bytes into session-media under their SHA-256 name.
func (f *retentionFixture) snapshot(name string, age time.Duration, copied bool) string {
	f.t.Helper()
	data := []byte("pixels of " + name)
	path := filepath.Join(f.snapDir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		f.t.Fatal(err)
	}
	when := f.now.Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		f.t.Fatal(err)
	}
	if copied {
		sum := sha256.Sum256(data)
		if err := os.WriteFile(filepath.Join(f.mediaDir, hex.EncodeToString(sum[:])+".png"), data, 0o600); err != nil {
			f.t.Fatal(err)
		}
	}
	return path
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestPruneSnapshotsKeepsNewestAndUncopied(t *testing.T) {
	f := newRetentionFixture(t)
	day := 24 * time.Hour
	// 22 recent snapshots: the newest 20 plus two recent extras, all copied.
	var recent []string
	for i := 0; i < 22; i++ {
		recent = append(recent, f.snapshot(fmt.Sprintf("screen-%d.png", 1000+i), time.Duration(i)*time.Minute, true))
	}
	oldCopied := []string{
		f.snapshot("screen-1.png", 8*day, true),
		f.snapshot("desktop-2.png", 9*day, true),
		f.snapshot("window-3.jpg", 10*day, true),
		f.snapshot("clipboard-4.png", 30*day, true),
	}
	oldUncopied := f.snapshot("screen-5.png", 30*day, false)
	// Never touched: foreign names, even when old and present in session-media.
	foreign := []string{
		f.snapshot("notes.txt", 30*day, true),
		f.snapshot(".capture-6.png", 30*day, true),
		f.snapshot("screen-abc.png", 30*day, true),
		f.snapshot("screen-7.png.bak", 30*day, true),
		f.snapshot("my-screen-8.png", 30*day, true),
	}
	// A directory with a matching name is never removed.
	subdir := filepath.Join(f.snapDir, "screen-9.png")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Same size but different bytes as a copied file: must be kept (hash check).
	sameSize := filepath.Join(f.snapDir, "screen-10.png")
	if err := os.WriteFile(sameSize, []byte("pixels of screen-0.png"), 0o600); err != nil { // same length as "pixels of screen-1.png"
		t.Fatal(err)
	}
	old := f.now.Add(-20 * day)
	if err := os.Chtimes(sameSize, old, old); err != nil {
		t.Fatal(err)
	}

	removed := pruneSnapshots(f.snapDir, filepath.Join(f.base, snapshotSessionMediaDir), f.now)
	if removed != len(oldCopied) {
		t.Fatalf("removed=%d, want %d", removed, len(oldCopied))
	}
	for _, path := range oldCopied {
		if exists(path) {
			t.Errorf("old copied snapshot kept: %s", path)
		}
	}
	for _, path := range append(append(recent, oldUncopied, sameSize, subdir), foreign...) {
		if !exists(path) {
			t.Errorf("snapshot removed but must be kept: %s", path)
		}
	}
}

func TestPruneSnapshotsAlwaysKeepsTwentyNewest(t *testing.T) {
	f := newRetentionFixture(t)
	var paths []string
	for i := 0; i < 25; i++ {
		// All older than 7 days and all copied: only the count rule protects them.
		paths = append(paths, f.snapshot(fmt.Sprintf("screen-%d.png", i), time.Duration(8*24+i)*time.Hour, true))
	}
	if removed := pruneSnapshots(f.snapDir, filepath.Join(f.base, snapshotSessionMediaDir), f.now); removed != 5 {
		t.Fatalf("removed=%d, want 5", removed)
	}
	for i, path := range paths {
		if want := i < snapshotRetentionKeep; exists(path) != want {
			t.Errorf("%s exists=%v, want %v", filepath.Base(path), !want, want)
		}
	}
}

func TestPruneSnapshotsWithoutSessionMediaDeletesNothing(t *testing.T) {
	f := newRetentionFixture(t)
	if err := os.RemoveAll(filepath.Join(f.base, snapshotSessionMediaDir)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		f.snapshot(fmt.Sprintf("screen-%d.png", i), 30*24*time.Hour, false)
	}
	if removed := pruneSnapshots(f.snapDir, filepath.Join(f.base, snapshotSessionMediaDir), f.now); removed != 0 {
		t.Fatalf("removed=%d, want 0", removed)
	}
	entries, _ := os.ReadDir(f.snapDir)
	if len(entries) != 30 {
		t.Fatalf("entries=%d, want 30", len(entries))
	}
}

// Copying bytes does not prove the original snapshot filename is unreferenced.
// Capture must defer deletion until the idle session-aware reference census.
func TestSendScreenshotSaveDefersPruningToSessionAwareMaintenance(t *testing.T) {
	f := newRetentionFixture(t)
	var old []string
	for i := 0; i < 25; i++ {
		old = append(old, f.snapshot(fmt.Sprintf("clipboard-%d.png", i), time.Duration(8*24+i)*time.Hour, true))
	}
	tool := NewSendScreenshot(f.base, nil)
	tool.Capture = fakeCapture{data: pngHeader, mediaType: "image/png"}
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	var meta struct{ Path string }
	if err := json.Unmarshal([]byte(res.Text), &meta); err != nil || !exists(meta.Path) {
		t.Fatalf("new snapshot missing: %q err=%v", meta.Path, err)
	}
	// Even copied old snapshots can still be referenced by saved tool results.
	kept := 0
	for _, path := range old {
		if exists(path) {
			kept++
		}
	}
	if kept != len(old) {
		t.Fatalf("kept old=%d, want %d before reference census", kept, len(old))
	}
}
