package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func newCapsuleTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestTaskLogCapsuleSQLiteFTSAndDeferredMirror(t *testing.T) {
	for _, recovery := range []string{"mutation", "reopen"} {
		t.Run(recovery, func(t *testing.T) {
			s := newCapsuleTestStore(t)
			created := time.Unix(1700000000, 0).UTC()
			if err := s.Put(Entry{ID: "capsule", Scope: ScopeTaskLog, Content: "oldcapsule", CreatedAt: created}); err != nil {
				t.Fatal(err)
			}
			path, _, err := ScopeFile(s.markdownRoot(), ScopeTaskLog)
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			s.beforeMirrorWrite = func(string) error {
				t.Error("capsule attempted synchronous Markdown rendering")
				return errInjectedMirrorFailure
			}
			// Holding the renderer lock must not postpone the SQL/FTS capsule.
			s.mirrorMu.Lock()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- s.SaveTaskLogCapsule(ctx, Entry{
					ID: "capsule", Scope: ScopeTaskLog, Content: "newcapsule", Source: SourceAgent, Tags: []string{"session"},
				}, MaxTaskLogEntries)
			}()
			select {
			case err := <-done:
				s.mirrorMu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				s.mirrorMu.Unlock()
				cancel()
				<-done
				t.Fatal("capsule waited for the Markdown renderer")
			}
			got, err := s.Get("capsule")
			if err != nil || got.Content != "newcapsule" || !got.CreatedAt.Equal(created) || got.Source != SourceAgent || got.TagsCSV() != "session" {
				t.Fatalf("authoritative capsule = %+v, err=%v", got, err)
			}
			found, err := s.Search("newcapsule", 5)
			if err != nil || len(found) != 1 || found[0].ID != "capsule" {
				t.Fatalf("immediate FTS = %+v, err=%v", found, err)
			}
			if err := s.SaveTaskLogCapsule(ctx, Entry{ID: "capsule", Scope: ScopeTaskLog, Content: "latestcapsule"}, MaxTaskLogEntries); err != nil {
				t.Fatal(err)
			}
			var generation int
			if err := s.db.QueryRow("SELECT generation FROM memory_mirror_outbox WHERE scope = ?", ScopeTaskLog).Scan(&generation); err != nil || generation != 2 {
				t.Fatalf("durable outbox generation = %d, err=%v", generation, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("mirror changed in terminal save: err=%v", err)
			}
			assertMemoryUsage(t, s)
			s.beforeMirrorWrite = nil
			if recovery == "reopen" {
				home := s.Root()
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				s, err = OpenStore(home)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
			} else if err := s.Put(Entry{ID: "ordinary", Scope: ScopeFact, Content: "ordinary mutation"}); err != nil {
				t.Fatal(err)
			}
			got, err = s.Get("capsule")
			if err != nil || got.LineStart == 0 || !got.CreatedAt.Equal(created) {
				t.Fatalf("recovered position/date = %+v, err=%v", got, err)
			}
			mirror, err := mdRead(path)
			// Markdown has one timestamp: mdWrite renders UpdatedAt and mdRead
			// exposes it as CreatedAt. SQLite preserves the original creation date.
			if err != nil || len(mirror) != 1 || mirror[0].Content != "latestcapsule" || !mirror[0].CreatedAt.Equal(got.UpdatedAt) {
				t.Fatalf("recovered mirror = %+v, err=%v", mirror, err)
			}
			if pendingMirrorCount(t, s) != 0 {
				t.Fatal("mirror work was not acknowledged")
			}
			assertMemoryUsage(t, s)
		})
	}
}

func TestTaskLogCapsuleRetentionRollsBackWithRejectedReplacement(t *testing.T) {
	s := newCapsuleTestStore(t)
	for i, id := range []string{"old", "middle", "newest"} {
		if err := s.Put(Entry{ID: id, Scope: ScopeTaskLog, Content: "keep " + id, CreatedAt: time.Unix(int64(i+1), 0)}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec("UPDATE memory_entries SET updated_at=? WHERE id=?", i+1, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`CREATE TABLE memory_vectors(id TEXT PRIMARY KEY, dim INTEGER NOT NULL, vec BLOB NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO memory_vectors SELECT id,1,X'00000000' FROM memory_entries`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER capsule_reject BEFORE INSERT ON memory_entries
 WHEN new.id='old' BEGIN SELECT RAISE(ABORT,'injected capsule failure'); END`); err != nil {
		t.Fatal(err)
	}
	entry := Entry{ID: "old", Scope: ScopeTaskLog, Content: "replacement"}
	if err := s.SaveTaskLogCapsule(context.Background(), entry, 2); err == nil || !strings.Contains(err.Error(), "injected capsule failure") {
		t.Fatalf("replacement error = %v", err)
	}
	for _, table := range []string{"memory_entries", "memory_fts", "memory_vectors"} {
		var count int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 3 {
			t.Fatalf("rollback %s = %d, err=%v", table, count, err)
		}
	}
	old, err := s.Get("old")
	if err != nil || old.Content != "keep old" || old.CreatedAt.Unix() != 1 {
		t.Fatalf("original capsule lost on failure: %+v, %v", old, err)
	}
	if pendingMirrorCount(t, s) != 0 {
		t.Fatal("rejected capsule committed mirror work")
	}
	assertMemoryUsage(t, s)
	if _, err := s.db.Exec("DROP TRIGGER capsule_reject"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTaskLogCapsule(context.Background(), entry, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("middle"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old tail was not removed: %v", err)
	}
	if current, err := s.Get("old"); err != nil || current.Content != "replacement" || current.CreatedAt.Unix() != 1 {
		t.Fatalf("replacement = %+v, err=%v", current, err)
	}
	if _, err := s.Get("newest"); err != nil {
		t.Fatal("newest retained log was lost:", err)
	}
	for _, table := range []string{"memory_fts", "memory_vectors"} {
		var count int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table + " WHERE id='middle'").Scan(&count); err != nil || count != 0 {
			t.Fatalf("retained stale %s row: count=%d err=%v", table, count, err)
		}
	}
	assertMemoryUsage(t, s)
}

func TestTaskLogCapsuleRejectsContentAndStoreLimits(t *testing.T) {
	s := newCapsuleTestStore(t)
	valid := Entry{ID: "capsule", Scope: ScopeTaskLog, Content: "bounded capsule"}
	for _, keep := range []int{0, MaxTaskLogEntries + 1} {
		if err := s.SaveTaskLogCapsule(context.Background(), valid, keep); err == nil {
			t.Fatalf("invalid keep=%d accepted", keep)
		}
	}
	oversized := valid
	oversized.Content = strings.Repeat("x", MaxEntryContentBytes+1)
	if err := s.SaveTaskLogCapsule(context.Background(), oversized, MaxTaskLogEntries); err == nil {
		t.Fatal("oversized capsule accepted")
	}
	if _, err := s.db.Exec(`WITH RECURSIVE numbers(n) AS (
 VALUES(0) UNION ALL SELECT n+1 FROM numbers WHERE n+1<?)
 INSERT INTO memory_entries(id,scope,file_path,content,created_at,updated_at)
 SELECT printf('fact-%04d',n),'fact','','bounded fact',1,1 FROM numbers`, MaxStoreEntries); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTaskLogCapsule(context.Background(), valid, MaxTaskLogEntries); err == nil || !strings.Contains(err.Error(), "store entry limit") {
		t.Fatalf("global entry limit error=%v", err)
	}
	if pendingMirrorCount(t, s) != 0 {
		t.Fatal("rejected capsule enqueued work")
	}
	if _, err := s.Get(valid.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rejected capsule committed: %v", err)
	}
	assertMemoryUsage(t, s)
}

func TestTaskLogCapsuleKeepsAtMostTaskLogLimit(t *testing.T) {
	s := newCapsuleTestStore(t)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < MaxTaskLogEntries; i++ {
		id := fmt.Sprintf("log-%03d", i)
		if _, err := tx.Exec(`INSERT INTO memory_entries(id,scope,file_path,content,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
			id, ScopeTaskLog, "", id, i+1, i+1); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec("INSERT INTO memory_fts(id,scope,content,tags) VALUES(?,?,?,'')", id, ScopeTaskLog, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTaskLogCapsule(context.Background(), Entry{ID: "current", Scope: ScopeTaskLog, Content: "latest"}, MaxTaskLogEntries); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"memory_entries", "memory_fts"} {
		var count int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE scope=?", ScopeTaskLog).Scan(&count); err != nil || count != MaxTaskLogEntries {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	if _, err := s.Get("log-000"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("oldest task log was retained: %v", err)
	}
	if _, err := s.Get("current"); err != nil {
		t.Fatalf("current capsule lost: %v", err)
	}
	assertMemoryUsage(t, s)
}

func TestTaskLogCapsuleFailsFastOnStoreWriter(t *testing.T) {
	s := newCapsuleTestStore(t)
	s.writeMu.Lock()
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		done <- s.SaveTaskLogCapsule(ctx, Entry{ID: "capsule", Scope: ScopeTaskLog, Content: "current"}, MaxTaskLogEntries)
	}()
	select {
	case err := <-done:
		s.writeMu.Unlock()
		if !errors.Is(err, ErrTaskLogCapsuleBusy) {
			t.Fatalf("contended Store writer error=%v", err)
		}
	case <-time.After(time.Second):
		s.writeMu.Unlock()
		cancel()
		<-done
		t.Fatal("capsule waited behind another Store writer")
	}
	if pendingMirrorCount(t, s) != 0 {
		t.Fatal("busy writer changed outbox")
	}
}

func TestTaskLogCapsulePrivateSQLiteConnectionFailsFast(t *testing.T) {
	s := newCapsuleTestStore(t)
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	err = s.SaveTaskLogCapsule(ctx, Entry{ID: "capsule", Scope: ScopeTaskLog, Content: "current"}, MaxTaskLogEntries)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("independent SQLite writer conflict was ignored")
	}
	if elapsed >= time.Second || ctx.Err() != nil {
		t.Fatalf("SQLite contention waited instead of failing fast: elapsed=%v ctx=%v err=%v", elapsed, ctx.Err(), err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var sharedTimeout int
	if err := conn.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&sharedTimeout); err != nil || sharedTimeout != 5000 {
		t.Fatalf("shared busy_timeout changed to %d: %v", sharedTimeout, err)
	}
	if pendingMirrorCount(t, s) != 0 {
		t.Fatal("SQLite conflict committed mirror work")
	}
	if err := s.SaveTaskLogCapsule(ctx, Entry{ID: "capsule", Scope: ScopeTaskLog, Content: "current"}, MaxTaskLogEntries); err != nil {
		t.Fatalf("uncontended retry: %v", err)
	}
}

func TestTaskLogCapsuleCanceledAndExpiredContextsDoNotWrite(t *testing.T) {
	s := newCapsuleTestStore(t)
	entry := Entry{ID: "capsule", Scope: ScopeTaskLog, Content: "current"}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.SaveTaskLogCapsule(canceled, entry, MaxTaskLogEntries); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error=%v", err)
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if err := s.SaveTaskLogCapsule(expired, entry, MaxTaskLogEntries); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired deadline error=%v", err)
	}
	if _, err := s.Get(entry.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("canceled capsule committed: %v", err)
	}
	if pendingMirrorCount(t, s) != 0 {
		t.Fatal("canceled capsule enqueued mirror work")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTaskLogCapsule(context.Background(), entry, MaxTaskLogEntries); err == nil {
		t.Fatal("closed Store was reopened")
	}
}

func TestTaskLogCapsuleDeadlineInterruptsSQLAndRollsBack(t *testing.T) {
	s := newCapsuleTestStore(t)
	if err := s.Put(Entry{ID: "previous", Scope: ScopeTaskLog, Content: "previous capsule"}); err != nil {
		t.Fatal(err)
	}
	// A deliberately long SQL statement tests cancellation inside the transaction,
	// after retention has already run, rather than merely at method entry.
	if _, err := s.db.Exec(`CREATE TRIGGER capsule_slow BEFORE INSERT ON memory_entries
 WHEN new.id='slow' BEGIN
 SELECT sum(n) FROM (
  WITH RECURSIVE numbers(n) AS (VALUES(0) UNION ALL SELECT n+1 FROM numbers WHERE n<10000000)
  SELECT n FROM numbers);
 END`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := s.SaveTaskLogCapsule(ctx, Entry{ID: "slow", Scope: ScopeTaskLog, Content: "slow capsule"}, 1)
	if err == nil || !errors.Is(ctx.Err(), context.DeadlineExceeded) || time.Since(started) >= time.Second {
		t.Fatalf("SQL deadline was not bounded: elapsed=%v ctx=%v err=%v", time.Since(started), ctx.Err(), err)
	}
	if previous, err := s.Get("previous"); err != nil || previous.Content != "previous capsule" {
		t.Fatalf("cancellation lost old retained log: %+v err=%v", previous, err)
	}
	if _, err := s.Get("slow"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("canceled SQL committed capsule: %v", err)
	}
	if pendingMirrorCount(t, s) != 0 {
		t.Fatal("canceled transaction committed mirror work")
	}
	assertMemoryUsage(t, s)
}
