package memory

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStore_PutGet_RoundTrip(t *testing.T) {
	s := openTestStore(t)
	e := Entry{ID: "a1", Scope: "general", Content: "hello world", Source: SourceUser, Tags: []string{"greet"}}
	if err := s.Put(e); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get("a1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Content != "hello world" {
		t.Errorf("Content = %q", got.Content)
	}
	if got.Scope != "general" {
		t.Errorf("Scope = %q", got.Scope)
	}
	if got.FilePath == "" {
		t.Errorf("FilePath empty")
	}
	if got.LineStart == 0 {
		t.Errorf("LineStart = 0, want > 0")
	}
	if !strings.Contains(got.FilePath, "general.md") {
		t.Errorf("FilePath = %q, want suffix general.md", got.FilePath)
	}
}

func TestStore_PutRejectsOversizedEntryBeforeWritingMirror(t *testing.T) {
	s := openTestStore(t)
	err := s.Put(Entry{ID: "huge", Scope: ScopeFact, Content: strings.Repeat("x", MaxEntryContentBytes+1), Source: SourceAgent})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("want entry size error, got %v", err)
	}
	path, _, _ := ScopeFile(s.markdownRoot(), ScopeFact)
	if _, statErr := osStat(path); !errIsNotExist(statErr) {
		t.Fatalf("oversized entry created a mirror: %v", statErr)
	}
}

func TestStore_Put_DuplicateIdReplaces(t *testing.T) {
	s := openTestStore(t)
	s.Put(Entry{ID: "a1", Scope: "general", Content: "first", Source: SourceUser})
	s.Put(Entry{ID: "a1", Scope: "general", Content: "second", Source: SourceUser})
	all, _ := s.List("general", 0)
	if len(all) != 1 {
		t.Fatalf("List size = %d, want 1", len(all))
	}
	if all[0].Content != "second" {
		t.Errorf("Content = %q", all[0].Content)
	}
}

func TestStore_ConcurrentPutsDoNotLoseMarkdownEntries(t *testing.T) {
	s := openTestStore(t)
	var wg sync.WaitGroup
	errCh := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errCh <- s.Put(Entry{ID: fmt.Sprintf("parallel-%02d", i), Scope: ScopeFact, Content: fmt.Sprintf("fact %d", i), Source: SourceAgent})
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	entries, err := s.List(ScopeFact, 0)
	if err != nil || len(entries) != 24 {
		t.Fatalf("sqlite entries=%d err=%v", len(entries), err)
	}
	path, _, _ := ScopeFile(s.markdownRoot(), ScopeFact)
	mirror, err := mdRead(path)
	if err != nil || len(mirror) != 24 {
		t.Fatalf("markdown entries=%d err=%v", len(mirror), err)
	}
}

func TestStore_Delete(t *testing.T) {
	s := openTestStore(t)
	s.Put(Entry{ID: "a1", Scope: "general", Content: "x", Source: SourceUser})
	if err := s.Delete("a1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get("a1"); err == nil {
		t.Errorf("Get after Delete: expected error")
	}
	all, _ := s.List("general", 0)
	if len(all) != 0 {
		t.Errorf("List = %v, want empty", all)
	}
}

func TestStore_DeleteMissing_NoError(t *testing.T) {
	s := openTestStore(t)
	if err := s.Delete("nope"); err != nil {
		t.Errorf("Delete missing: %v", err)
	}
}

func TestStore_RetainKeepsNewestAndRewritesMirrorOnce(t *testing.T) {
	s := openTestStore(t)
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("log-%d", i)
		if err := s.Put(Entry{ID: id, Scope: ScopeTaskLog, Content: id, Source: SourceAgent, CreatedAt: time.Unix(int64(i), 0)}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	removed, err := s.Retain(ScopeTaskLog, 2)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 3 {
		t.Fatalf("removed=%d want 3", removed)
	}
	entries, err := s.List(ScopeTaskLog, 0)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
	if entries[0].ID != "log-5" || entries[1].ID != "log-4" {
		t.Fatalf("retained wrong entries: %+v", entries)
	}
	path, _, _ := ScopeFile(s.markdownRoot(), ScopeTaskLog)
	data, err := readFile(t, path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(data, "log-1") || !strings.Contains(data, "log-5") {
		t.Fatalf("mirror not retained correctly: %s", data)
	}
}

func TestStore_List_ByScope(t *testing.T) {
	s := openTestStore(t)
	s.Put(Entry{ID: "1", Scope: "general", Content: "a", Source: SourceUser})
	s.Put(Entry{ID: "2", Scope: "general", Content: "b", Source: SourceUser})
	s.Put(Entry{ID: "3", Scope: "project:abc12345", Content: "c", Source: SourceUser})
	got, _ := s.List("general", 0)
	if len(got) != 2 {
		t.Errorf("general size = %d, want 2", len(got))
	}
}

func TestStore_Search_FTS(t *testing.T) {
	s := openTestStore(t)
	s.Put(Entry{ID: "1", Scope: "general", Content: "Bubble Tea is the TUI framework we use", Source: SourceUser})
	s.Put(Entry{ID: "2", Scope: "general", Content: "Lip Gloss is a styling library", Source: SourceUser})
	hits, err := s.Search("Bubble", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	if hits[0].ID != "1" {
		t.Errorf("hit = %+v", hits[0])
	}
}

func TestStore_Search_EmptyQuery(t *testing.T) {
	s := openTestStore(t)
	s.Put(Entry{ID: "1", Scope: "general", Content: "x", Source: SourceUser})
	hits, err := s.Search("", 5)
	if err != nil {
		t.Errorf("Search empty: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("hits = %v, want empty", hits)
	}
}

func TestStore_RecentBudgeted_RespectsCap(t *testing.T) {
	s := openTestStore(t)
	// 3 entries ~ 100 tokens each (~400 chars), cap 150 tokens
	// means only the first (newest) should fit.
	big := strings.Repeat("word ", 100) // 500 chars ~ 125 tokens
	s.Put(Entry{ID: "a", Scope: "general", Content: big, Source: SourceUser, CreatedAt: time.Unix(100, 0)})
	s.Put(Entry{ID: "b", Scope: "general", Content: big, Source: SourceUser, CreatedAt: time.Unix(99, 0)})
	s.Put(Entry{ID: "c", Scope: "general", Content: big, Source: SourceUser, CreatedAt: time.Unix(98, 0)})
	out, err := s.RecentBudgeted("general", 150)
	if err != nil {
		t.Fatalf("RecentBudgeted: %v", err)
	}
	if !strings.Contains(out, "[a]") {
		t.Errorf("output = %q, want [a]", out)
	}
	if strings.Contains(out, "[b]") || strings.Contains(out, "[c]") {
		t.Errorf("output = %q, should not contain b or c (over budget)", out)
	}
}

func TestStore_AppendScratch(t *testing.T) {
	s := openTestStore(t)
	if err := s.AppendScratch("first thought"); err != nil {
		t.Fatalf("AppendScratch: %v", err)
	}
	if err := s.AppendScratch("second thought"); err != nil {
		t.Fatalf("AppendScratch: %v", err)
	}
	date := time.Now().UTC().Format("2006-01-02")
	all, _ := s.List("scratch:"+date, 0)
	if len(all) != 2 {
		t.Errorf("scratch count = %d, want 2", len(all))
	}
}

func TestStore_ByTag(t *testing.T) {
	s := openTestStore(t)
	s.Put(Entry{ID: "1", Scope: "general", Content: "x", Source: SourceUser, Tags: []string{"urgent"}})
	s.Put(Entry{ID: "2", Scope: "general", Content: "y", Source: SourceUser, Tags: []string{"nice-to-have"}})
	hits, _ := s.ByTag("urgent", 10)
	if len(hits) != 1 || hits[0].ID != "1" {
		t.Errorf("ByTag = %+v", hits)
	}
}

func TestStore_ArchiveOldScratches(t *testing.T) {
	s := openTestStore(t)
	// Create a scratch file dated 60 days ago.
	old := time.Now().UTC().AddDate(0, 0, -60).Format("2006-01-02")
	path, _, _ := ScopeFile(s.markdownRoot(), "scratch:"+old)
	if err := mdUpsert(path, Entry{ID: "old-1", Scope: "scratch:" + old, Content: "stale", Source: SourceAgent}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	n, err := s.ArchiveOldScratches(context.Background())
	if err != nil {
		t.Fatalf("ArchiveOldScratches: %v", err)
	}
	if n != 1 {
		t.Errorf("archived = %d, want 1", n)
	}
	if _, err := osStat(path); !errIsNotExist(err) {
		t.Errorf("old file still exists: %v", err)
	}
	archived := filepath.Join(s.markdownRoot(), "archive", "scratch-"+old+".md")
	if _, err := osStat(archived); err != nil {
		t.Errorf("archived missing: %v", err)
	}
}

func TestStore_ArchiveOldScratches_NoOpOnFresh(t *testing.T) {
	s := openTestStore(t)
	if err := s.AppendScratch("today"); err != nil {
		t.Fatalf("AppendScratch: %v", err)
	}
	n, _ := s.ArchiveOldScratches(context.Background())
	if n != 0 {
		t.Errorf("archived = %d, want 0 (file is fresh)", n)
	}
}

func TestStore_MarkdownSync_FileContainsEntry(t *testing.T) {
	s := openTestStore(t)
	s.Put(Entry{ID: "abc", Scope: "general", Content: "first line\nsecond line", Source: SourceAgent})
	path, _, _ := ScopeFile(s.markdownRoot(), "general")
	data, err := readFile(t, path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(data, "## abc") {
		t.Errorf("file does not contain header: %q", data)
	}
	if !strings.Contains(data, "first line") {
		t.Errorf("file does not contain content: %q", data)
	}
}

func TestStore_MarkdownSync_DeleteRemovesFromFile(t *testing.T) {
	s := openTestStore(t)
	s.Put(Entry{ID: "a", Scope: "general", Content: "x", Source: SourceUser})
	s.Put(Entry{ID: "b", Scope: "general", Content: "y", Source: SourceUser})
	s.Delete("a")
	path, _, _ := ScopeFile(s.markdownRoot(), "general")
	data, _ := readFile(t, path)
	if strings.Contains(data, "## a") {
		t.Errorf("file still contains a: %q", data)
	}
	if !strings.Contains(data, "## b") {
		t.Errorf("file does not contain b: %q", data)
	}
}
