package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteRowsCleanupCannotDeleteRecreatedSIDMedia(t *testing.T) {
	for _, all := range []bool{false, true} {
		name := "one"
		if all {
			name = "all"
		}
		t.Run(name, func(t *testing.T) {
			s := openTestStore(t)
			sess := receiptSession(t, s)
			ctx := context.Background()
			old, _, err := s.storeSessionImage(sess.ID, "image/png", []byte("old media"))
			if err != nil {
				t.Fatal(err)
			}
			unknown, _, err := s.storeSessionImage("unknown-orphan-sid", "image/png", []byte("preserved unknown media"))
			if err != nil {
				t.Fatal(err)
			}
			var cleanup func() error
			if all {
				var removed int
				removed, cleanup, err = s.DeleteAllRows(ctx)
				if removed != 1 {
					t.Fatalf("removed=%d, want 1", removed)
				}
			} else {
				cleanup, err = s.DeleteRows(ctx, sess.ID)
			}
			if err != nil || cleanup == nil {
				t.Fatalf("delete err=%v cleanup nil=%v", err, cleanup == nil)
			}
			if _, err := os.Stat(old); !os.IsNotExist(err) {
				t.Fatalf("old media was not detached before cleanup: %v", err)
			}
			// Recreate through the actual public API before cleanup executes.
			if err := s.EnsureSession(sess.ID, sess.Cwd, "replacement"); err != nil {
				t.Fatal(err)
			}
			fresh, _, err := s.storeSessionImage(sess.ID, "image/png", []byte("fresh media"))
			if err != nil {
				t.Fatal(err)
			}
			if err := cleanup(); err != nil {
				t.Fatal(err)
			}
			if err := cleanup(); err != nil {
				t.Fatalf("cleanup retry=%v", err)
			}
			if got, err := os.ReadFile(fresh); err != nil || string(got) != "fresh media" {
				t.Fatalf("replacement media removed or changed: %q err=%v", got, err)
			}
			if got, err := os.ReadFile(unknown); err != nil || string(got) != "preserved unknown media" {
				t.Fatalf("unknown media removed or changed: %q err=%v", got, err)
			}
		})
	}
}

func TestDeleteRowsCommitFailureRestoresQuarantinedMedia(t *testing.T) {
	for _, all := range []bool{false, true} {
		s := openTestStore(t)
		sess := receiptSession(t, s)
		for _, statement := range []string{
			`CREATE TABLE delete_guard_parent(id INTEGER PRIMARY KEY)`,
			`CREATE TABLE delete_guard(child INTEGER REFERENCES delete_guard_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
			`CREATE TRIGGER reject_delete_commit AFTER DELETE ON sessions BEGIN INSERT INTO delete_guard(child) VALUES(99); END`,
		} {
			if _, err := s.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
		old, _, err := s.storeSessionImage(sess.ID, "image/png", []byte("restorable media"))
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		var cleanup func() error
		if all {
			_, cleanup, err = s.DeleteAllRows(ctx)
		} else {
			cleanup, err = s.DeleteRows(ctx, sess.ID)
		}
		if err == nil || cleanup != nil {
			t.Fatalf("failed commit returned cleanup: all=%v err=%v", all, err)
		}
		if _, err := s.Get(sess.ID); err != nil {
			t.Fatalf("failed commit removed SQL session: %v", err)
		}
		if got, err := os.ReadFile(old); err != nil || string(got) != "restorable media" {
			t.Fatalf("failed commit lost original media: %q err=%v", got, err)
		}
		if pending, err := s.HasMediaDeleteRecovery(); err != nil || pending {
			t.Fatalf("successful rollback retained quarantine: pending=%v err=%v", pending, err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := s.DeleteRows(canceled, sess.ID); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel=%v", err)
		}
	}
}

func TestQuarantineRollbackPreservesUnexpectedNewDirectory(t *testing.T) {
	root := t.TempDir()
	original, directory := filepath.Join(root, "original"), filepath.Join(root, ".deleted-original-unique")
	for _, path := range []string{original, directory} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(original, "new"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "old"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restoreQuarantinedSessionMedia([]quarantinedSessionMedia{{original: original, directory: directory}}); err == nil {
		t.Fatal("occupied rollback path silently accepted")
	}
	for path, want := range map[string]string{filepath.Join(original, "new"): "new", filepath.Join(directory, "old"): "old"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("rollback overwrote data: %q err=%v", got, err)
		}
	}
}
