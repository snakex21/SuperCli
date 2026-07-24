package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var errInjectedMirrorFailure = errors.New("injected mirror failure")

func failNextMirror(s *Store) {
	fired := false
	s.beforeMirrorWrite = func(string) error {
		if fired {
			return nil
		}
		fired = true
		return errInjectedMirrorFailure
	}
}

func pendingMirrorCount(t *testing.T, s *Store) int {
	t.Helper()
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memory_mirror_outbox`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestStorePutMirrorFailureRecoversOnOpen(t *testing.T) {
	home := t.TempDir()
	s, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	failNextMirror(s)
	err = s.Put(Entry{ID: "durable-put", Scope: ScopeFact, Content: "SQLite survived", Source: SourceAgent})
	if !errors.Is(err, errInjectedMirrorFailure) {
		t.Fatalf("Put error = %v, want injected failure", err)
	}
	if got, getErr := s.Get("durable-put"); getErr != nil || got.Content != "SQLite survived" {
		t.Fatalf("authoritative row after failed mirror = %+v, err=%v", got, getErr)
	}
	if pendingMirrorCount(t, s) != 1 {
		t.Fatal("failed Put did not leave durable mirror work")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = OpenStore(home)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	got, err := s.Get("durable-put")
	if err != nil || got.LineStart == 0 || got.FilePath == "" {
		t.Fatalf("reconciled row = %+v, err=%v", got, err)
	}
	mirror, err := mdRead(got.FilePath)
	if err != nil || len(mirror) != 1 || mirror[0].Content != "SQLite survived" {
		t.Fatalf("reconciled mirror = %+v, err=%v", mirror, err)
	}
	if pendingMirrorCount(t, s) != 0 {
		t.Fatal("startup reconciliation did not acknowledge mirror work")
	}
}

func TestStoreDeleteMirrorFailureRecoversOnOpen(t *testing.T) {
	home := t.TempDir()
	s, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Entry{ID: "durable-delete", Scope: ScopeFact, Content: "remove me", Source: SourceAgent}); err != nil {
		t.Fatal(err)
	}
	path, _, _ := ScopeFile(s.markdownRoot(), ScopeFact)
	failNextMirror(s)
	err = s.Delete("durable-delete")
	if !errors.Is(err, errInjectedMirrorFailure) {
		t.Fatalf("Delete error = %v, want injected failure", err)
	}
	if _, getErr := s.Get("durable-delete"); getErr == nil {
		t.Fatal("SQLite delete was not committed before mirror failure")
	}
	before, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(before), "durable-delete") {
		t.Fatalf("expected stale pre-crash mirror, read=%q err=%v", before, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = OpenStore(home)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	if mirror, readErr := mdRead(path); readErr != nil || len(mirror) != 0 {
		t.Fatalf("deleted entry returned after reconciliation: %+v err=%v", mirror, readErr)
	}
	if pendingMirrorCount(t, s) != 0 {
		t.Fatal("delete mirror work remains after startup recovery")
	}
}

func TestStoreClearMirrorFailureRecoversOnOpen(t *testing.T) {
	home := t.TempDir()
	s, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []Entry{
		{ID: "clear-a", Scope: ScopeFact, Content: "a", Source: SourceAgent},
		{ID: "clear-b", Scope: ScopePreference, Content: "b", Source: SourceAgent},
	} {
		if err := s.Put(entry); err != nil {
			t.Fatal(err)
		}
	}
	failNextMirror(s)
	removed, err := s.Clear()
	if removed != 2 || !errors.Is(err, errInjectedMirrorFailure) {
		t.Fatalf("Clear removed=%d err=%v", removed, err)
	}
	if entries, listErr := s.List("", 0); listErr != nil || len(entries) != 0 {
		t.Fatalf("SQLite after failed Clear = %+v err=%v", entries, listErr)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = OpenStore(home)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	var markdown []string
	err = filepath.WalkDir(s.markdownRoot(), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".md") {
			markdown = append(markdown, path)
		}
		return walkErr
	})
	if err != nil || len(markdown) != 0 {
		t.Fatalf("stale mirrors after Clear recovery = %v err=%v", markdown, err)
	}
	if pendingMirrorCount(t, s) != 0 {
		t.Fatal("clear mirror work remains after startup recovery")
	}
}

func TestOpenStoreReconcilesTamperedAndStaleMarkdown(t *testing.T) {
	home := t.TempDir()
	s, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Entry{ID: "truth", Scope: ScopeFact, Content: "from SQLite", Source: SourceAgent}); err != nil {
		t.Fatal(err)
	}
	path, _, _ := ScopeFile(s.markdownRoot(), ScopeFact)
	stale := filepath.Join(s.markdownRoot(), "stale.md")
	if err := os.WriteFile(path, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("not in SQLite"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = OpenStore(home)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "from SQLite") || strings.Contains(string(data), "tampered") {
		t.Fatalf("canonical mirror = %q err=%v", data, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale mirror still exists: %v", err)
	}
}

func TestIndependentRenderersCannotLeaveOlderMirrorAfterNewerAck(t *testing.T) {
	home := t.TempDir()
	older, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer older.Close()
	newer, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer newer.Close()
	// Separate processMirrorLock values model two OS processes: the only shared
	// serialization left is the lock held by SQLite itself.
	older.mirrorMu = &sync.Mutex{}
	newer.mirrorMu = &sync.Mutex{}

	readOlder := make(chan struct{})
	releaseOlder := make(chan struct{})
	var once sync.Once
	older.afterMirrorRead = func(string) {
		once.Do(func() {
			close(readOlder)
			<-releaseOlder
		})
	}
	olderDone := make(chan error, 1)
	go func() {
		olderDone <- older.Put(Entry{ID: "race", Scope: ScopeFact, Content: "generation one", Source: SourceAgent})
	}()
	<-readOlder

	newerDone := make(chan error, 1)
	go func() {
		newerDone <- newer.Put(Entry{ID: "race", Scope: ScopeFact, Content: "generation two", Source: SourceAgent})
	}()
	select {
	case err := <-newerDone:
		close(releaseOlder)
		<-olderDone
		t.Fatalf("newer process bypassed active mirror transaction: %v", err)
	case <-time.After(150 * time.Millisecond):
		// Expected: generation two cannot commit while generation one's
		// snapshot and filesystem replacement are still in progress.
	}
	close(releaseOlder)
	if err := <-olderDone; err != nil {
		t.Fatalf("older Put: %v", err)
	}
	if err := <-newerDone; err != nil {
		t.Fatalf("newer Put: %v", err)
	}

	path, _, _ := ScopeFile(older.markdownRoot(), ScopeFact)
	mirror, err := mdRead(path)
	if err != nil || len(mirror) != 1 || mirror[0].Content != "generation two" {
		t.Fatalf("final mirror = %+v err=%v", mirror, err)
	}
	if pendingMirrorCount(t, older) != 0 {
		t.Fatal("newest generation was not acknowledged")
	}
}

func TestStaleCleanupCannotDeleteNewScopeFromIndependentProcess(t *testing.T) {
	home := t.TempDir()
	cleaner, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer cleaner.Close()
	writer, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	// Model separate processes, which do not share processMirrorLock.
	cleaner.mirrorMu = &sync.Mutex{}
	writer.mirrorMu = &sync.Mutex{}

	readExpected := make(chan struct{})
	releaseCleaner := make(chan struct{})
	cleaner.afterStaleRead = func() {
		close(readExpected)
		<-releaseCleaner
	}
	cleanDone := make(chan error, 1)
	go func() {
		cleaner.writeMu.Lock()
		defer cleaner.writeMu.Unlock()
		cleanDone <- cleaner.removeStaleMarkdownLocked()
	}()
	<-readExpected

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writer.Put(Entry{ID: "fresh", Scope: "fresh", Content: "new scope", Source: SourceAgent})
	}()
	select {
	case err := <-writeDone:
		close(releaseCleaner)
		<-cleanDone
		t.Fatalf("new scope bypassed active stale-cleanup transaction: %v", err)
	case <-time.After(150 * time.Millisecond):
		// Expected: the new SQLite scope cannot commit until cleanup finishes.
	}
	close(releaseCleaner)
	if err := <-cleanDone; err != nil {
		t.Fatalf("stale cleanup: %v", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("new scope Put: %v", err)
	}

	path, _, _ := ScopeFile(cleaner.markdownRoot(), "fresh")
	mirror, err := mdRead(path)
	if err != nil || len(mirror) != 1 || mirror[0].Content != "new scope" {
		t.Fatalf("new scope mirror = %+v err=%v", mirror, err)
	}
	if pendingMirrorCount(t, cleaner) != 0 {
		t.Fatal("new scope was left pending after cleanup")
	}
}
