package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestOpen_CreatesNewDB(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	want := filepath.Join(home, DataDirName, DBFileName)
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected db file at %q: %v", want, err)
	}
}

func TestOpen_OpensExisting(t *testing.T) {
	home := t.TempDir()
	db1, err := Open(home)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := db1.Exec(`CREATE TABLE sample (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("create sample: %v", err)
	}
	if _, err := db1.Exec(`INSERT INTO sample(name) VALUES ('hello')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	db1.Close()

	db2, err := Open(home)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db2.Close()

	var n string
	if err := db2.QueryRow(`SELECT name FROM sample LIMIT 1`).Scan(&n); err != nil {
		t.Fatalf("select: %v", err)
	}
	if n != "hello" {
		t.Fatalf("got %q, want hello", n)
	}
}

func TestOpen_SchemaVersionRecorded(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var v int
	if err := db.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&v); err != nil {
		t.Fatalf("select version: %v", err)
	}
	if v != schemaVersion {
		t.Fatalf("schema version = %d, want %d", v, schemaVersion)
	}
}

func TestOpen_ForeignKeysEnabled(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	var on int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if on != 1 {
		t.Fatalf("foreign_keys = %d, want 1", on)
	}
}

func TestOpen_EmptyHome(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatalf("expected error on empty home")
	}
}

func TestOpen_Concurrent(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE counter (n INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO counter(n) VALUES (0)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const workers = 8
	const perWorker = 50
	done := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			tx, err := db.Begin()
			if err != nil {
				done <- err
				return
			}
			for j := 0; j < perWorker; j++ {
				if _, err := tx.Exec(`UPDATE counter SET n = n + 1`); err != nil {
					_ = tx.Rollback()
					done <- err
					return
				}
			}
			done <- tx.Commit()
		}()
	}
	for i := 0; i < workers; i++ {
		if err := <-done; err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT n FROM counter`).Scan(&n); err != nil {
		t.Fatalf("select: %v", err)
	}
	if n != workers*perWorker {
		t.Fatalf("counter = %d, want %d", n, workers*perWorker)
	}
}

func TestMigrate_RejectsNewerSchema(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := db.Exec(`UPDATE schema_version SET version = ?`, schemaVersion+10); err != nil {
		t.Fatalf("bump version: %v", err)
	}
	db.Close()

	if _, err := Open(home); err == nil {
		t.Fatalf("expected error on newer schema")
	}
}

func TestMigrate_ReopensAtSameVersion(t *testing.T) {
	home := t.TempDir()
	db1, err := Open(home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	db1.Close()
	db2, err := Open(home)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
}

// ensure sql.ErrNoRows vs scan mismatch still returns error.
func TestMigrate_QueryRowOnEmptyTable(t *testing.T) {
	home := t.TempDir()
	db, err := Open(home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	var s string
	err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='nope'`).Scan(&s)
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}
