package memory

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func assertMemoryUsage(t *testing.T, s *Store) {
	t.Helper()
	var count, bytes, wantCount, wantBytes int64
	if err := s.db.QueryRow(`SELECT entries,content_bytes FROM memory_store_usage WHERE id=1`).Scan(&count, &bytes); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(length(CAST(content AS BLOB))),0) FROM memory_entries`).Scan(&wantCount, &wantBytes); err != nil {
		t.Fatal(err)
	}
	if count != wantCount || bytes != wantBytes {
		t.Fatalf("usage=(%d,%d) actual=(%d,%d)", count, bytes, wantCount, wantBytes)
	}
}
func TestMemoryUsageTracksIndependentWritersAndRollback(t *testing.T) {
	home := t.TempDir()
	first, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for i, store := range []*Store{first, second} {
		if err := store.Put(Entry{ID: "shared", Scope: ScopeFact, Content: strings.Repeat("żółw 🐢", i+1)}); err != nil {
			t.Fatal(err)
		}
		assertMemoryUsage(t, first)
		assertMemoryUsage(t, second)
	}
	if _, err := second.db.Exec(`UPDATE memory_entries SET content=? WHERE id='shared'`, "你好"); err != nil {
		t.Fatal(err)
	}
	assertMemoryUsage(t, first)
	tx, err := first.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO memory_entries(id,scope,file_path,content,created_at,updated_at) VALUES('rollback','fact','','temporary',1,1)`); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertMemoryUsage(t, second)
	for i := 0; i < 3; i++ {
		if err := first.Put(Entry{ID: fmt.Sprintf("log-%d", i), Scope: ScopeTaskLog, Content: "log"}); err != nil {
			t.Fatal(err)
		}
	}
	if removed, err := second.Retain(ScopeTaskLog, 1); err != nil || removed != 2 {
		t.Fatalf("Retain=%d,%v", removed, err)
	}
	assertMemoryUsage(t, first)
	if err := second.Delete("shared"); err != nil {
		t.Fatal(err)
	}
	assertMemoryUsage(t, first)
	if count, err := first.Clear(); err != nil || count != 1 {
		t.Fatalf("Clear=%d,%v", count, err)
	}
	assertMemoryUsage(t, second)
}
func TestMemoryUsageMigratesExistingDatabaseOnce(t *testing.T) {
	home := t.TempDir()
	s, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Entry{ID: "legacy", Scope: ScopeFact, Content: "trwała notatka\ndruga linia"}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	for _, sql := range []string{`DROP TRIGGER memory_usage_insert`, `DROP TRIGGER memory_usage_delete`, `DROP TRIGGER memory_usage_content`, `DROP TABLE memory_store_usage`} {
		if _, err := s.db.Exec(sql); err != nil {
			s.Close()
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s, err = OpenStore(home)
		if err != nil {
			t.Fatal(err)
		}
		assertMemoryUsage(t, s)
		entry, err := s.Get("legacy")
		if err != nil || entry.Content != "trwała notatka\ndruga linia" || entry.LineStart == 0 {
			s.Close()
			t.Fatalf("lost migrated note: %+v %v", entry, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
func TestMemoryCapacityIndependentWritersCannotBothTakeLastSlot(t *testing.T) {
	first := seedMemoryWriteFixture(t, MaxStoreEntries-1, "existing")
	second, err := OpenStore(first.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	start := make(chan struct{})
	done := make(chan error, 2)
	for i, store := range []*Store{first, second} {
		go func(i int, store *Store) {
			<-start
			done <- store.Put(Entry{ID: fmt.Sprintf("last-slot-%d", i), Scope: ScopeFact, Content: "last slot"})
		}(i, store)
	}
	close(start)
	successes, full := 0, 0
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err == nil {
				successes++
			} else if strings.Contains(err.Error(), "store entry limit") {
				full++
			} else {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent writes did not finish")
		}
	}
	if successes != 1 || full != 1 {
		t.Fatalf("successes=%d full=%d", successes, full)
	}
	assertMemoryUsage(t, first)
	// Replacing an existing entry at capacity must remain allowed.
	if err := second.Put(Entry{ID: "note-00000", Scope: ScopeFact, Content: "replacement"}); err != nil {
		t.Fatal(err)
	}
	assertMemoryUsage(t, first)
}

func TestMemoryContentLimitCountsUTF8BytesAndDiscountsReplacement(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	body := strings.Repeat("ż", MaxEntryContentBytes/2)
	count := MaxStoreContentBytes / len(body)
	if _, err := s.db.Exec(`WITH RECURSIVE numbers(n) AS (
 VALUES(0) UNION ALL SELECT n+1 FROM numbers WHERE n+1<?)
 INSERT INTO memory_entries(id,scope,file_path,content,created_at,updated_at)
 SELECT printf('seed-%04d',n),'fact','',?,1,1 FROM numbers`, count, body); err != nil {
		t.Fatal(err)
	}
	assertMemoryUsage(t, s)
	if _, err := checkMemoryCapacity(s.db, Entry{ID: "seed-0000", Content: body}); err != nil {
		t.Fatalf("replacement at byte limit: %v", err)
	}
	if err := s.Put(Entry{ID: "overflow", Scope: ScopeFact, Content: "x"}); err == nil || !strings.Contains(err.Error(), "store content limit") {
		t.Fatalf("Put at byte limit: %v", err)
	}
	assertMemoryUsage(t, s)
	var overflow int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memory_entries WHERE id='overflow'`).Scan(&overflow); err != nil || overflow != 0 {
		t.Fatalf("failed Put changed entries: %d %v", overflow, err)
	}
}
