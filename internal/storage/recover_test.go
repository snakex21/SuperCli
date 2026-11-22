package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A damaged supercli.db is moved aside with its -wal file, never deleted,
// and the app starts on a fresh database (1.0.39).
func TestOpenAtRecoversFromCorruptDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DBFileName)
	garbage := bytes.Repeat([]byte("this is not an sqlite database "), 200)
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", []byte("stale wal"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := OpenAt(dir)
	if err != nil {
		t.Fatalf("a damaged database stopped the start: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE probe (x INTEGER)`); err != nil {
		t.Fatalf("fresh database unusable: %v", err)
	}
	var rec *Recovery
	for _, r := range Recoveries() {
		if r.File == path {
			r := r
			rec = &r
		}
	}
	if rec == nil {
		t.Fatal("the recovery was not recorded for the GUI")
	}
	kept, err := os.ReadFile(rec.Backup)
	if err != nil || !bytes.Equal(kept, garbage) {
		t.Fatalf("the damaged data was not kept byte for byte at %s: %v", rec.Backup, err)
	}
	if wal, err := os.ReadFile(rec.Backup + "-wal"); err != nil || string(wal) != "stale wal" {
		t.Fatalf("the -wal file was not kept: %v", err)
	}
}

// A locked database is never moved aside; the error says what to do.
func TestOpenRecoveringLeavesLockedDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.db")
	if err := os.WriteFile(path, []byte(sqliteHeader+"in use"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, err := OpenRecovering(path, func() (int, error) {
		calls++
		return 0, errors.New("ping: database is locked (5) (SQLITE_BUSY)")
	})
	if err == nil || !strings.Contains(err.Error(), "locked by another process") {
		t.Fatalf("locked error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("open retried %d times on a locked database", calls)
	}
	if b, _ := os.ReadFile(path); string(b) != sqliteHeader+"in use" {
		t.Fatal("a locked database was moved or changed")
	}
}

// Other errors (a missing directory, permissions) are returned unchanged
// and touch nothing.
func TestOpenRecoveringPassesOtherErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "y.db")
	_ = os.WriteFile(path, []byte(sqliteHeader+"ok"), 0o600)
	want := errors.New("permission denied")
	if _, err := OpenRecovering(path, func() (int, error) { return 0, want }); !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("file moved on an unrelated error")
	}
}

// sqliteHeader makes a fixture look like SQLite to the pre-open check.
const sqliteHeader = "SQLite format 3\x00"
