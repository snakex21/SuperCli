package session

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pragmaInt(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	var v int64
	if err := db.QueryRow("PRAGMA " + name).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return v
}

func TestOpenStoreNewDatabaseUsesIncrementalAutoVacuum(t *testing.T) {
	s := openTestStore(t)
	if got := pragmaInt(t, s.db, "auto_vacuum"); got != 2 {
		t.Fatalf("auto_vacuum=%d, want 2 (incremental)", got)
	}
	if got := pragmaInt(t, s.db, "journal_size_limit"); got != sessionJournalSizeLimit {
		t.Fatalf("journal_size_limit=%d, want %d", got, sessionJournalSizeLimit)
	}
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode=%q err=%v", mode, err)
	}
	// The mode must survive reopening (it is a property of the file).
	root := s.Root()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := pragmaInt(t, reopened.db, "auto_vacuum"); got != 2 {
		t.Fatalf("reopened auto_vacuum=%d, want 2", got)
	}
}

// A legacy auto_vacuum=NONE database is opened unchanged: no implicit VACUUM
// conversion runs at startup.
func TestOpenStoreLegacyDatabaseKeepsAutoVacuumNone(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE legacy_marker(x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := pragmaInt(t, s.db, "auto_vacuum"); got != 0 {
		t.Fatalf("legacy auto_vacuum=%d, want 0", got)
	}
	sess, err := s.Create("/cwd", "model", "t")
	if err != nil {
		t.Fatal(err)
	}
	// Deletion still succeeds; incremental_vacuum is a harmless no-op here.
	if err := s.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteRowsRunsIncrementalVacuum(t *testing.T) {
	for _, all := range []bool{false, true} {
		name := "one"
		if all {
			name = "all"
		}
		t.Run(name, func(t *testing.T) {
			s := openTestStore(t)
			keep, err := s.Create("/cwd", "model", "keep")
			if err != nil {
				t.Fatal(err)
			}
			victim, err := s.Create("/cwd", "model", "victim")
			if err != nil {
				t.Fatal(err)
			}
			payload := strings.Repeat("unbounded growth payload ", 400) // ~10 KB
			now := time.Now().UTC().UnixNano()
			for seq := 0; seq < 300; seq++ {
				if _, err := s.db.Exec(`INSERT INTO messages(session_id, seq, role, content, created_at) VALUES (?,?,?,?,?)`,
					victim.ID, seq, "user", payload, now); err != nil {
					t.Fatal(err)
				}
			}
			before := pragmaInt(t, s.db, "page_count")
			ctx := context.Background()
			var cleanup func() error
			if all {
				_, cleanup, err = s.DeleteAllRows(ctx)
			} else {
				cleanup, err = s.DeleteRows(ctx, victim.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := cleanup(); err != nil {
				t.Fatal(err)
			}
			after := pragmaInt(t, s.db, "page_count")
			if free := pragmaInt(t, s.db, "freelist_count"); free != 0 {
				t.Fatalf("freelist_count=%d after delete, want 0 (incremental_vacuum not run)", free)
			}
			// ~3 MB of rows were deleted; the logical file must shrink well below that.
			if after >= before || (before-after)*pragmaInt(t, s.db, "page_size") < 2<<20 {
				t.Fatalf("page_count before=%d after=%d: freed pages were not reclaimed", before, after)
			}
			if !all {
				if _, err := s.Get(keep.ID); err != nil {
					t.Fatalf("unrelated session lost: %v", err)
				}
			}
			var check string
			if err := s.db.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
				t.Fatalf("integrity_check=%q err=%v", check, err)
			}
		})
	}
}
