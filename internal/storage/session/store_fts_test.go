package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
)

// These fixtures stay beside the package rather than in a profile temp dir.
// No test opens the application's real sessions.db.
func openFTSTestStore(t *testing.T) *Store {
	t.Helper()
	dir, err := os.MkdirTemp(".", ".session-fts-test-")
	if err != nil {
		t.Fatal(err)
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func ftsExec(t *testing.T, db messagesFTSExecutor, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("synthetic FTS SQL: %v", err)
	}
}

func ftsFixture(t *testing.T, s *Store) {
	t.Helper()
	for _, session := range []struct{ id, cwd string }{{"fts-a", "fts-workspace"}, {"fts-b", "fts-workspace"}, {"fts-c", "fts-other"}} {
		if err := s.EnsureSession(session.id, session.cwd, "fts-model"); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		id  string
		msg Encoded
	}{
		{"fts-a", Encoded{Role: "user", Content: "running café migration alpha"}},
		{"fts-a", Encoded{Role: "tool", Content: "run migration beta beta", ToolCallID: "fts-tool"}},
		{"fts-a", Encoded{Role: "assistant", PartsJSON: `[{"type":"text","text":"partsonly"}]`, ToolCallsJSON: `[{"id":"fts-tool","name":"synthetic","arguments":{"text":"argumentonly"}}]`}},
		{"fts-a", Encoded{Role: "user", Content: "ephemeral zebra"}},
		{"fts-a", Encoded{Role: "user", Content: "longer migration migration gamma"}},
		{"fts-b", Encoded{Role: "user", Content: "migration café"}},
		{"fts-b", Encoded{Role: "system", Content: "system migration delta"}},
		{"fts-c", Encoded{Role: "user", Content: "foreign migration"}},
	} {
		if err := s.AppendMessage(context.Background(), row.id, row.msg); err != nil {
			t.Fatal(err)
		}
	}
}

// Exact table/trigger behavior of the pre-migration F13 implementation. Do not
// derive this fixture from the new trigger definitions: older binaries must
// recognize the table and leave the new trigger SQL alone.
const ftsLegacyTable = `CREATE VIRTUAL TABLE messages_fts USING fts5(
	content,
	tokenize = 'porter unicode61 remove_diacritics 2'
)`

var ftsLegacyTriggers = [...]string{
	`CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
		INSERT INTO messages_fts(rowid, content) VALUES (new.id, COALESCE(new.content, ''));
	END`,
	`CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
		DELETE FROM messages_fts WHERE rowid = old.id;
	END`,
	`CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
		DELETE FROM messages_fts WHERE rowid = old.id;
		INSERT INTO messages_fts(rowid, content) VALUES (new.id, COALESCE(new.content, ''));
	END`,
}

func ftsDropIndex(t *testing.T, db messagesFTSExecutor) {
	t.Helper()
	for _, name := range []string{"messages_ai", "messages_ad", "messages_au"} {
		ftsExec(t, db, `DROP TRIGGER IF EXISTS `+name)
	}
	ftsExec(t, db, `DROP TABLE messages_fts`)
}

func ftsInstallLegacy(t *testing.T, s *Store) {
	t.Helper()
	ftsDropIndex(t, s.db)
	ftsExec(t, s.db, ftsLegacyTable)
	for _, query := range ftsLegacyTriggers {
		ftsExec(t, s.db, query)
	}
	ftsExec(t, s.db, `INSERT INTO messages_fts(rowid, content) SELECT id, COALESCE(content, '') FROM messages`)
}

func ftsAssertCurrent(t *testing.T, s *Store) {
	t.Helper()
	state, err := readMessagesFTSState(context.Background(), s.db)
	if err != nil || !state.current {
		t.Fatalf("external FTS schema: %+v, %v", state, err)
	}
	var copies int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='messages_fts_content'`).Scan(&copies); err != nil {
		t.Fatal(err)
	}
	if copies != 0 {
		t.Fatal("external FTS still stores a duplicate content table")
	}
	ftsExec(t, s.db, messagesFTSIntegrity)
}

func ftsAssertMatches(t *testing.T, s *Store, term string, want int) {
	t.Helper()
	// Query the index without a messages join so an orphan index entry cannot
	// be hidden by the application search's join.
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH ?`, term).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("MATCH %q = %d, want %d", term, count, want)
	}
}

type ftsRankHit struct {
	ID         int64
	Rank, BM25 float64
	Snippet    string
}

type ftsSearchSnapshot struct {
	History []HistoryHit
	Grouped []HistoryHit
	Ranked  []ftsRankHit
	Rows    []Encoded
	Page    []Encoded
	More    bool
}

func ftsSnapshot(t *testing.T, s *Store) ftsSearchSnapshot {
	t.Helper()
	ctx := context.Background()
	var result ftsSearchSnapshot
	var err error
	result.History, err = s.SearchHistory(ctx, "migration OR cafe OR running", "", "", time.Time{}, time.Time{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	result.Grouped, err = s.SearchSessionMatches(ctx, "migration", []string{"fts-workspace"}, "fts-a", 20)
	if err != nil {
		t.Fatal(err)
	}
	result.Rows, err = s.ReadMessages(ctx, "fts-a")
	if err != nil {
		t.Fatal(err)
	}
	result.Page, result.More, err = s.ReadMessagesBefore(ctx, "fts-a", 5, 2)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Query(`SELECT m.id, f.rank, bm25(messages_fts),
		snippet(messages_fts, 0, '<mark>', '</mark>', '...', 16)
		FROM messages_fts f JOIN messages m ON m.id=f.rowid
		WHERE messages_fts MATCH 'migration OR cafe OR running' ORDER BY rank, m.created_at DESC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var hit ftsRankHit
		if err := rows.Scan(&hit.ID, &hit.Rank, &hit.BM25, &hit.Snippet); err != nil {
			t.Fatal(err)
		}
		result.Ranked = append(result.Ranked, hit)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMessagesFTSExternalLifecycle(t *testing.T) {
	s := openFTSTestStore(t)
	ftsFixture(t, s)
	ftsAssertCurrent(t, s)
	ftsAssertMatches(t, s, "running", 2) // Porter stemming remains enabled.
	ftsAssertMatches(t, s, "cafe", 2)    // Existing diacritic folding remains enabled.
	ftsAssertMatches(t, s, "partsonly OR argumentonly", 0)

	ftsExec(t, s.db, `UPDATE messages SET content='edited zebra' WHERE session_id='fts-a' AND seq=4`)
	ftsAssertMatches(t, s, "ephemeral", 0)
	ftsAssertMatches(t, s, "edited", 1)
	// OLD.content must be supplied even after the source row is NULL or empty.
	ftsExec(t, s.db, `UPDATE messages SET content=NULL WHERE session_id='fts-a' AND seq=4`)
	ftsAssertMatches(t, s, "edited", 0)
	ftsExec(t, s.db, `UPDATE messages SET content='' WHERE session_id='fts-a' AND seq=4`)
	ftsExec(t, s.db, `UPDATE messages SET content='restored zebra' WHERE session_id='fts-a' AND seq=4`)
	ftsExec(t, s.db, `UPDATE messages SET id=id+1000 WHERE session_id='fts-a' AND seq=4`)
	ftsAssertCurrent(t, s)

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ftsExec(t, tx, `INSERT INTO messages(session_id,seq,role,content,created_at) VALUES('fts-a',6,'user','rollbackinsert',123)`)
	ftsExec(t, tx, `UPDATE messages SET content='rollbackedit' WHERE session_id='fts-a' AND seq=1`)
	ftsExec(t, tx, `DELETE FROM messages WHERE session_id='fts-b'`)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	ftsAssertMatches(t, s, "rollbackinsert OR rollbackedit", 0)
	ftsAssertMatches(t, s, "running", 2)
	ftsAssertCurrent(t, s)
	if removed, err := s.TruncateFrom(context.Background(), "fts-a", 4); err != nil || removed != 2 {
		t.Fatalf("truncate: %d, %v", removed, err)
	}
	ftsAssertMatches(t, s, "restored OR gamma", 0)
	branch, err := s.Fork(context.Background(), "fts-a", 0, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ftsAssertMatches(t, s, "alpha", 2)
	if err := s.Delete(branch.ID); err != nil {
		t.Fatal(err)
	}
	ftsAssertMatches(t, s, "alpha", 1)
	if err := s.Delete("fts-a"); err != nil {
		t.Fatal(err)
	}
	ftsAssertMatches(t, s, "alpha OR beta", 0)
	ftsAssertCurrent(t, s)
	if err := s.DeleteAll(); err != nil {
		t.Fatal(err)
	}
	ftsAssertMatches(t, s, "migration", 0)
	ftsAssertCurrent(t, s)
}

func TestOpenStoreMigratesLegacyFTSWithoutChangingHistoryOrRanking(t *testing.T) {
	s := openFTSTestStore(t)
	ftsFixture(t, s)
	ftsInstallLegacy(t, s)
	before := ftsSnapshot(t, s)
	if len(before.History) == 0 || len(before.Grouped) != 1 || !before.More {
		t.Fatal("migration fixture did not exercise search, grouping and pagination")
	}
	root := s.Root()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ftsAssertCurrent(t, s)
	if after := ftsSnapshot(t, s); !reflect.DeepEqual(before, after) {
		t.Fatalf("migration changed history, rank, BM25, snippet or page:\nbefore=%+v\nafter=%+v", before, after)
	}
	var versionBefore, versionAfter int
	if err := s.db.QueryRow(`PRAGMA schema_version`).Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureMessagesFTS(); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`PRAGMA schema_version`).Scan(&versionAfter); err != nil || versionBefore != versionAfter {
		t.Fatalf("idempotent schema check performed DDL: %d -> %d, %v", versionBefore, versionAfter, err)
	}

	// Simulate the old bootstrap on a separate connection, then write/delete
	// in its original source-table style. Old code checks only table existence.
	old, err := sql.Open("sqlite", filepath.Join(root, "sessions.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	var hasFTS int
	if err := old.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='messages_fts'`).Scan(&hasFTS); err != nil {
		t.Fatal(err)
	}
	if hasFTS == 0 {
		ftsExec(t, old, ftsLegacyTable)
		for _, query := range ftsLegacyTriggers {
			ftsExec(t, old, query)
		}
	}
	ftsAssertCurrent(t, s)
	ftsExec(t, old, `INSERT INTO messages(session_id,seq,role,content,created_at) VALUES('fts-a',6,'user','oldwriterinsert',123)`)
	ftsAssertMatches(t, s, "oldwriterinsert", 1)
	ftsExec(t, old, `UPDATE messages SET content='oldwriteredit' WHERE session_id='fts-a' AND seq=6`)
	ftsAssertMatches(t, s, "oldwriterinsert", 0)
	ftsAssertMatches(t, s, "oldwriteredit", 1)
	ftsExec(t, old, `DELETE FROM messages WHERE session_id='fts-a' AND seq=6`)
	ftsAssertMatches(t, s, "oldwriteredit", 0)
	ftsExec(t, old, `DELETE FROM sessions WHERE id='fts-b'`)
	ftsAssertCurrent(t, s)
}

func TestOpenStoreBackfillsMissingFTS(t *testing.T) {
	s := openFTSTestStore(t)
	ftsFixture(t, s)
	before := ftsSnapshot(t, s)
	ftsDropIndex(t, s.db)
	root := s.Root()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ftsAssertCurrent(t, s)
	if after := ftsSnapshot(t, s); !reflect.DeepEqual(before, after) {
		t.Fatal("creating the missing FTS index did not backfill existing messages")
	}
}

// Inject an actual orphan index entry immediately before the real rank=1
// check. This proves a failed consistency check rolls back all preceding DDL
// and the successful rebuild, rather than testing an early mock error only.
type corruptFTSCandidate struct {
	*sql.Conn
	injected bool
}

func (c *corruptFTSCandidate) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if query == messagesFTSIntegrity {
		c.injected = true
		if _, err := c.Conn.ExecContext(ctx, `INSERT INTO messages_fts(rowid,content) VALUES(999999,'candidateghost')`); err != nil {
			return nil, err
		}
	}
	return c.Conn.ExecContext(ctx, query, args...)
}

func TestMessagesFTSMigrationIntegrityFailureRestoresLegacyIndexAndTriggers(t *testing.T) {
	s := openFTSTestStore(t)
	ftsFixture(t, s)
	ftsInstallLegacy(t, s)
	before := ftsSnapshot(t, s)
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate := &corruptFTSCandidate{Conn: conn}
	err = migrateMessagesFTS(context.Background(), candidate)
	if closeErr := conn.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if !candidate.injected || err == nil || !strings.Contains(err.Error(), "verify messages FTS against messages") {
		t.Fatalf("expected external consistency failure after rebuild, got injected=%v, err=%v", candidate.injected, err)
	}
	var table string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='messages_fts'`).Scan(&table); err != nil {
		t.Fatal(err)
	}
	if normalizeMessagesFTSSQL(table) != normalizeMessagesFTSSQL(ftsLegacyTable) {
		t.Fatal("failed migration did not restore the legacy FTS table")
	}
	for _, name := range []string{"messages_ai", "messages_ad", "messages_au"} {
		var definition string
		if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if name != "messages_ai" && !strings.Contains(definition, "DELETE FROM messages_fts WHERE rowid = old.id") {
			t.Fatalf("legacy trigger %s was not restored", name)
		}
	}
	ftsAssertMatches(t, s, "candidateghost", 0)
	if after := ftsSnapshot(t, s); !reflect.DeepEqual(before, after) {
		t.Fatal("failed migration changed source history or the old search results")
	}
	// The restored old triggers remain usable, and a later retry can migrate.
	ftsExec(t, s.db, `UPDATE messages SET content='afterfailure' WHERE session_id='fts-a' AND seq=4`)
	ftsAssertMatches(t, s, "ephemeral", 0)
	ftsAssertMatches(t, s, "afterfailure", 1)
	if err := s.ensureMessagesFTS(); err != nil {
		t.Fatal(err)
	}
	ftsAssertCurrent(t, s)
	ftsAssertMatches(t, s, "afterfailure", 1)
}

func failFTSMigrationOnIntegrity(ctx context.Context, executor messagesFTSExecutor) error {
	conn, ok := executor.(*sql.Conn)
	if !ok {
		return fmt.Errorf("fault fixture expected a pinned connection")
	}
	return migrateMessagesFTS(ctx, &corruptFTSCandidate{Conn: conn})
}

func TestMessagesFTSOptionalMigrationFallbackKeepsWriterAndRetries(t *testing.T) {
	s := openFTSTestStore(t)
	ftsFixture(t, s)
	ftsInstallLegacy(t, s)
	// The failed connection must relinquish its pool slot before fallback can
	// acquire a different connection. A leaked Conn would deadlock this test.
	s.db.SetMaxOpenConns(1)
	before := ftsSnapshot(t, s)
	if err := s.ensureMessagesFTSWithMigration(failFTSMigrationOnIntegrity); err != nil {
		t.Fatalf("valid restored legacy store was rejected: %v", err)
	}
	state, err := readMessagesFTSState(context.Background(), s.db)
	if err != nil || !state.legacy || state.current {
		t.Fatalf("fallback did not retain exact legacy schema: %+v, %v", state, err)
	}
	if after := ftsSnapshot(t, s); !reflect.DeepEqual(before, after) {
		t.Fatal("fallback changed stored history or search results")
	}
	writer := NewWriter(s, "fts-a")
	if err := writer.AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: "fallbackwriter"}); err != nil {
		t.Fatalf("fallback disabled session persistence: %v", err)
	}
	ftsAssertMatches(t, s, "fallbackwriter", 1)
	hits, err := s.SearchHistory(context.Background(), "fallbackwriter", "fts-a", "user", time.Time{}, time.Time{}, 20)
	if err != nil || len(hits) != 1 {
		t.Fatalf("fallback disabled history search: %+v, %v", hits, err)
	}
	if err := s.AppendMessage(context.Background(), "fts-a", Encoded{Role: "user", Content: "fallbackdelete"}); err != nil {
		t.Fatal(err)
	}
	ftsExec(t, s.db, `DELETE FROM messages WHERE session_id='fts-a' AND seq=7`)
	ftsAssertMatches(t, s, "fallbackdelete", 0)
	ftsExec(t, s.db, messagesFTSIntegrity)
	beforeRetry := ftsSnapshot(t, s)
	root := s.Root()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(root)
	if err != nil {
		t.Fatalf("next open did not retry migration: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ftsAssertCurrent(t, s)
	ftsAssertMatches(t, s, "fallbackwriter", 1)
	if afterRetry := ftsSnapshot(t, s); !reflect.DeepEqual(beforeRetry, afterRetry) {
		t.Fatal("retry migration changed history persisted during fallback")
	}
}

func TestMessagesFTSOptionalMigrationFallbackRejectsIncompleteLegacyStore(t *testing.T) {
	for _, fixture := range []struct {
		name         string
		breakLegacy  func(*testing.T, *Store)
		comparesData bool
	}{
		{"missing-trigger", func(t *testing.T, s *Store) {
			ftsExec(t, s.db, `DROP TRIGGER messages_au`)
		}, false},
		{"different-content", func(t *testing.T, s *Store) {
			ftsExec(t, s.db, `UPDATE messages_fts SET content='unsynchronized' WHERE rowid=(SELECT id FROM messages WHERE session_id='fts-a' AND seq=4)`)
		}, true},
		{"missing-row", func(t *testing.T, s *Store) {
			ftsExec(t, s.db, `DELETE FROM messages_fts WHERE rowid=(SELECT id FROM messages WHERE session_id='fts-a' AND seq=4)`)
		}, true},
		{"orphan-row", func(t *testing.T, s *Store) {
			ftsExec(t, s.db, `INSERT INTO messages_fts(rowid,content) VALUES(888888,'legacyorphan')`)
		}, true},
		{"corrupt-index", func(t *testing.T, s *Store) {
			// Keep the stored text, totals and segment directory, but remove
			// inverted-index leaves. Source-content equality alone is insufficient.
			ftsExec(t, s.db, `DELETE FROM messages_fts_data WHERE id>10`)
		}, false},
		{"missing-index", func(t *testing.T, s *Store) {
			ftsDropIndex(t, s.db)
		}, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			s := openFTSTestStore(t)
			ftsFixture(t, s)
			ftsInstallLegacy(t, s)
			fixture.breakLegacy(t, s)
			before, err := s.ReadMessages(context.Background(), "fts-a")
			if err != nil {
				t.Fatal(err)
			}
			err = s.ensureMessagesFTSWithMigration(failFTSMigrationOnIntegrity)
			if err == nil {
				t.Fatal("invalid or missing legacy index was accepted as fallback")
			}
			if fixture.comparesData && !strings.Contains(err.Error(), "legacy FTS has missing, orphaned or different message content") {
				t.Fatalf("fallback did not validate exact source content: %v", err)
			}
			if fixture.name == "corrupt-index" && !strings.Contains(err.Error(), "verify retained legacy FTS integrity") {
				t.Fatalf("fallback did not check the inverted index beyond matching source text: %v", err)
			}
			after, err := s.ReadMessages(context.Background(), "fts-a")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("failed fallback modified source history: %+v, %v", after, err)
			}
		})
	}
}

type failFTSRollback struct{ *corruptFTSCandidate }

func (c *failFTSRollback) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if query == "ROLLBACK" {
		return nil, fmt.Errorf("synthetic rollback failure")
	}
	return c.corruptFTSCandidate.ExecContext(ctx, query, args...)
}

func TestMessagesFTSOptionalMigrationFallbackRequiresConfirmedRollback(t *testing.T) {
	s := openFTSTestStore(t)
	ftsFixture(t, s)
	ftsInstallLegacy(t, s)
	s.db.SetMaxOpenConns(1)
	before := ftsSnapshot(t, s)
	err := s.ensureMessagesFTSWithMigration(func(ctx context.Context, executor messagesFTSExecutor) error {
		conn, ok := executor.(*sql.Conn)
		if !ok {
			return fmt.Errorf("fault fixture expected a pinned connection")
		}
		return migrateMessagesFTS(ctx, &failFTSRollback{&corruptFTSCandidate{Conn: conn}})
	})
	var failure *messagesFTSMigrationError
	if err == nil || !errors.As(err, &failure) || failure.rollbackConfirmed {
		t.Fatalf("unconfirmed rollback was accepted as fallback: %v", err)
	}
	// Closing the discarded SQLite connection may undo its active transaction,
	// but that does not substitute for an explicitly confirmed rollback.
	if after := ftsSnapshot(t, s); !reflect.DeepEqual(before, after) {
		t.Fatal("discarding an unconfirmed transaction did not preserve old history")
	}
}

func TestMessagesFTSRepairsObsoleteExternalTriggers(t *testing.T) {
	s := openFTSTestStore(t)
	ftsFixture(t, s)
	ftsExec(t, s.db, `DROP TRIGGER messages_ad`)
	ftsExec(t, s.db, ftsLegacyTriggers[1])
	ftsExec(t, s.db, `DELETE FROM messages WHERE session_id='fts-a' AND seq=4`)
	ftsAssertMatches(t, s, "ephemeral", 1) // A join would conceal this ghost.
	if err := s.ensureMessagesFTS(); err != nil {
		t.Fatal(err)
	}
	ftsAssertMatches(t, s, "ephemeral", 0)
	ftsAssertCurrent(t, s)
}

func TestMessagesFTSRejectsUnknownTokenizerWithoutResettingIndex(t *testing.T) {
	s := openFTSTestStore(t)
	ftsFixture(t, s)
	ftsDropIndex(t, s.db)
	ftsExec(t, s.db, `CREATE VIRTUAL TABLE messages_fts USING fts5(content, tokenize='unicode61')`)
	ftsExec(t, s.db, `INSERT INTO messages_fts(rowid,content) SELECT id,COALESCE(content,'') FROM messages`)
	before := ftsSnapshot(t, s)
	if err := s.ensureMessagesFTS(); err == nil || !strings.Contains(err.Error(), "unsupported messages FTS table schema") {
		t.Fatalf("expected preserved unknown schema, got %v", err)
	}
	if after := ftsSnapshot(t, s); !reflect.DeepEqual(before, after) {
		t.Fatal("unsupported schema handling modified history or index")
	}
	if opened, err := OpenStore(s.Root()); err == nil || opened != nil {
		if opened != nil {
			_ = opened.Close()
		}
		t.Fatalf("OpenStore silently accepted unsupported FTS: %v", err)
	}
}

type recordFTSStatements struct {
	*sql.Conn
	statements []string
	reserved   bool
}

func (c *recordFTSStatements) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	c.statements = append(c.statements, query)
	result, err := c.Conn.ExecContext(ctx, query, args...)
	if query == "BEGIN IMMEDIATE" && err == nil {
		c.reserved = true
	}
	return result, err
}

func (c *recordFTSStatements) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if !c.reserved {
		return nil, fmt.Errorf("schema read before writer reservation")
	}
	return c.Conn.QueryContext(ctx, query, args...)
}

func TestMessagesFTSMigrationRechecksAfterWriterReservation(t *testing.T) {
	s := openFTSTestStore(t)
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	recorder := &recordFTSStatements{Conn: conn}
	// The schema has already been migrated, as when a second opener acquired
	// the writer after another process committed the one-time migration.
	if err := migrateMessagesFTS(context.Background(), recorder); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recorder.statements, []string{"BEGIN IMMEDIATE", "COMMIT"}) {
		t.Fatalf("current schema rebuilt after waiting for writer: %v", recorder.statements)
	}
}
