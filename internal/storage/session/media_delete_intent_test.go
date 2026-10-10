package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func mediaCrashSession(t *testing.T, store *Store) (Session, string) {
	t.Helper()
	sess := receiptSession(t, store)
	ctx := context.Background()
	writer := NewWriter(store, sess.ID)
	image, err := writer.ExternalizeImage(ctx, "image/png", []byte("synthetic recoverable image"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{
		{Type: llm.PartTypeText, Text: "synthetic image prompt"},
		{Type: llm.PartTypeImage, Image: &image},
	}}); err != nil {
		t.Fatal(err)
	}
	return sess, image.Path
}

// Rollback is SQLite's crash outcome before COMMIT. Intentionally bypass the
// normal filesystem defer: reopen must recover durable files on its own.
func mediaCrashBeforeCommit(t *testing.T, store *Store, sess Session, rename bool) quarantinedSessionMedia {
	t.Helper()
	ctx := context.Background()
	if err := store.ensureMediaDeleteSchema(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sess.ID); err != nil {
		t.Fatal(err)
	}
	media, err := store.prepareMediaDelete(ctx, tx, filepath.Base(store.sessionMediaDir(sess.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if rename {
		if err := renameMediaDelete(media.original, media.directory); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	return media
}

func mediaCrashReopen(t *testing.T, store *Store) *Store {
	t.Helper()
	root := store.Root()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	return reopened
}

func mediaSingleIntent(t *testing.T, store *Store) quarantinedSessionMedia {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(store.Root(), mediaDeleteNamespace))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			id := strings.TrimSuffix(entry.Name(), ".json")
			media, err := store.readMediaDeleteIntent(filepath.Join(store.Root(), mediaDeleteNamespace, entry.Name()), id)
			if err != nil {
				t.Fatal(err)
			}
			return media
		}
	}
	t.Fatal("no synthetic intent")
	return quarantinedSessionMedia{}
}

func assertMediaRecoveryRetired(t *testing.T, store *Store) {
	t.Helper()
	if pending, err := store.HasMediaDeleteRecovery(); err != nil || pending {
		t.Fatalf("recovery namespace retained: pending=%v err=%v", pending, err)
	}
	var markers int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM session_media_deletions`).Scan(&markers); err != nil || markers != 0 {
		t.Fatalf("markers=%d err=%v", markers, err)
	}
	if err := store.RecoverMediaDeletes(context.Background()); err != nil {
		t.Fatalf("idempotent recovery=%v", err)
	}
}

func TestMediaDeleteRecoveryBeforeCommitRestoresExistingConversationAttachments(t *testing.T) {
	for _, renamed := range []bool{false, true} {
		t.Run(fmt.Sprintf("renamed_%v", renamed), func(t *testing.T) {
			store := openTestStore(t)
			sess, old := mediaCrashSession(t, store)
			media := mediaCrashBeforeCommit(t, store, sess, renamed)
			if renamed {
				if _, err := os.Stat(old); !os.IsNotExist(err) {
					t.Fatalf("fixture did not move old media: %v", err)
				}
			}
			store = mediaCrashReopen(t, store)
			if committed, err := store.mediaDeleteCommitted(context.Background(), media); err != nil || committed {
				t.Fatalf("uncommitted delete had marker: %v err=%v", committed, err)
			}
			// The caller holds StoreGate in production. These fixtures have no
			// concurrent actors and exercise the real SQL/filesystem boundary.
			if err := store.RecoverMediaDeletes(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Get(sess.ID); err != nil {
				t.Fatalf("crashed transaction removed conversation: %v", err)
			}
			if got, err := os.ReadFile(old); err != nil || string(got) != "synthetic recoverable image" {
				t.Fatalf("existing attachment unavailable after recovery: %q err=%v", got, err)
			}
			messages, err := store.ReadModelContext(context.Background(), sess.ID)
			if err != nil || len(messages) != 1 || len(messages[0].Parts) != 2 || messages[0].Parts[1].Image == nil || messages[0].Parts[1].Image.Path != old {
				t.Fatalf("attachment reference was not restored: %+v err=%v", messages, err)
			}
			assertMediaRecoveryRetired(t, store)
		})
	}
}

func TestMediaDeleteRecoveryAfterCommitCleansOnlyCapturedTree(t *testing.T) {
	store := openTestStore(t)
	sess, _ := mediaCrashSession(t, store)
	if _, err := store.DeleteRows(context.Background(), sess.ID); err != nil {
		t.Fatal(err)
	}
	media := mediaSingleIntent(t, store)
	store = mediaCrashReopen(t, store)
	if _, err := store.Get(sess.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("committed delete did not remove conversation: %v", err)
	}
	if err := store.EnsureSession(sess.ID, sess.Cwd, "replacement"); err != nil {
		t.Fatal(err)
	}
	fresh, _, err := store.storeSessionImage(sess.ID, "image/png", []byte("fresh replacement image"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecoverMediaDeletes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(fresh); err != nil || string(got) != "fresh replacement image" {
		t.Fatalf("committed cleanup touched replacement: %q err=%v", got, err)
	}
	if _, err := os.Stat(media.directory); !os.IsNotExist(err) {
		t.Fatalf("captured committed tree remains: %v", err)
	}
	assertMediaRecoveryRetired(t, store)
}

func TestMediaDeleteRecoveryPortableMovedRootRestoresAttachmentReference(t *testing.T) {
	store := openTestStore(t)
	sess, old := mediaCrashSession(t, store)
	mediaCrashBeforeCommit(t, store, sess, true)
	oldRoot := store.Root()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// Portable copies may omit an empty original parent after quarantine.
	if err := os.Remove(filepath.Join(oldRoot, sessionMediaDirName)); err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(t.TempDir(), "moved-application-data")
	if err := os.Rename(oldRoot, newRoot); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.RecoverMediaDeletes(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(reopened.sessionMediaDir(sess.ID), filepath.Base(old))
	messages, err := reopened.ReadModelContext(context.Background(), sess.ID)
	if err != nil || len(messages) != 1 || len(messages[0].Parts) != 2 || messages[0].Parts[1].Image == nil || messages[0].Parts[1].Image.Path != wantPath {
		t.Fatalf("moved attachment reference was not repaired: %+v err=%v", messages, err)
	}
	if got, err := os.ReadFile(wantPath); err != nil || string(got) != "synthetic recoverable image" {
		t.Fatalf("moved recovery lost bytes: %q err=%v", got, err)
	}
	assertMediaRecoveryRetired(t, reopened)
}

func TestMediaDeleteRecoveryOccupiedOriginalPreservesBothTrees(t *testing.T) {
	store := openTestStore(t)
	sess, old := mediaCrashSession(t, store)
	media := mediaCrashBeforeCommit(t, store, sess, true)
	fresh, _, err := store.storeSessionImage(sess.ID, "image/png", []byte("manual fresh image"))
	if err != nil {
		t.Fatal(err)
	}
	store = mediaCrashReopen(t, store)
	if err := store.RecoverMediaDeletes(context.Background()); !errors.Is(err, ErrMediaDeleteRecovery) {
		t.Fatalf("occupied original silently overwritten: %v", err)
	}
	for path, want := range map[string]string{fresh: "manual fresh image", filepath.Join(media.directory, filepath.Base(old)): "synthetic recoverable image"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("recovery lost bytes: %q err=%v", got, err)
		}
	}
	if _, err := os.Stat(media.journal); err != nil {
		t.Fatalf("blocked recovery lost its durable intent: %v", err)
	}
}

func TestMediaDeleteRecoveryAfterMarkerRetirementNeverTouchesNewOriginal(t *testing.T) {
	store := openTestStore(t)
	sess, _ := mediaCrashSession(t, store)
	if _, err := store.DeleteRows(context.Background(), sess.ID); err != nil {
		t.Fatal(err)
	}
	media := mediaSingleIntent(t, store)
	if err := os.RemoveAll(media.directory); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM session_media_deletions WHERE operation_id = ?`, media.intent.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSession(sess.ID, sess.Cwd, "new"); err != nil {
		t.Fatal(err)
	}
	fresh, _, err := store.storeSessionImage(sess.ID, "image/png", []byte("new original"))
	if err != nil {
		t.Fatal(err)
	}
	store = mediaCrashReopen(t, store)
	if err := store.RecoverMediaDeletes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(fresh); err != nil || string(got) != "new original" {
		t.Fatalf("retired marker touched new original: %q err=%v", got, err)
	}
	assertMediaRecoveryRetired(t, store)
}

func TestMediaDeletePartialCleanupAndSQLFailureKeepCommittedMarker(t *testing.T) {
	store := openTestStore(t)
	sess, old := mediaCrashSession(t, store)
	if _, _, err := store.storeSessionImage(sess.ID, "image/png", []byte("second captured image")); err != nil {
		t.Fatal(err)
	}
	cleanup, err := store.DeleteRows(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	media := mediaSingleIntent(t, store)
	partialErr := errors.New("synthetic partial remove failure")
	err = store.finishCommittedMediaDeleteWith(context.Background(), media, func(path string) error {
		if err := os.Remove(filepath.Join(path, filepath.Base(old))); err != nil {
			return err
		}
		return partialErr
	})
	if !errors.Is(err, partialErr) {
		t.Fatalf("partial failure=%v", err)
	}
	if committed, err := store.mediaDeleteCommitted(context.Background(), media); err != nil || !committed {
		t.Fatalf("partial cleanup lost marker: %v err=%v", committed, err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_media_marker_retire BEFORE DELETE ON session_media_deletions BEGIN SELECT RAISE(ABORT, 'synthetic marker retire failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err == nil {
		t.Fatal("SQL marker failure accepted")
	}
	if committed, err := store.mediaDeleteCommitted(context.Background(), media); err != nil || !committed {
		t.Fatalf("SQL failure lost marker: %v err=%v", committed, err)
	}
	if _, err := os.Stat(media.journal); err != nil {
		t.Fatalf("SQL failure lost intent: %v", err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_media_marker_retire`); err != nil {
		t.Fatal(err)
	}
	store = mediaCrashReopen(t, store)
	if err := store.RecoverMediaDeletes(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertMediaRecoveryRetired(t, store)
}

func TestMediaDeleteAbsentNamespaceDoesNotTouchSQLiteOrScanMedia(t *testing.T) {
	store := openTestStore(t)
	if _, _, err := store.storeSessionImage("unowned-original", "image/png", []byte("unowned data")); err != nil {
		t.Fatal(err)
	}
	// No marker table exists on a normal first open. Closing SQL also proves
	// the absent-namespace path does not attempt a query or lazy schema write.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if pending, err := store.HasMediaDeleteRecovery(); err != nil || pending {
		t.Fatalf("absent path pending=%v err=%v", pending, err)
	}
	if err := store.RecoverMediaDeletes(context.Background()); err != nil {
		t.Fatalf("absent path touched closed SQL: %v", err)
	}
}

func TestMediaDeleteRecoveryIncompleteStagingAndCanceledResume(t *testing.T) {
	store := openTestStore(t)
	sess, old := mediaCrashSession(t, store)
	mediaCrashBeforeCommit(t, store, sess, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.RecoverMediaDeletes(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled recovery=%v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("canceled recovery changed original: %v", err)
	}
	if err := store.RecoverMediaDeletes(context.Background()); err != nil {
		t.Fatal(err)
	}
	namespace := filepath.Join(store.Root(), mediaDeleteNamespace)
	if err := os.Mkdir(namespace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(namespace, strings.Repeat("a", 32)+".tmp"), []byte(`{"format":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.RecoverMediaDeletes(context.Background()); err != nil {
		t.Fatalf("metadata-only interrupted publication did not retire: %v", err)
	}
	if got, err := os.ReadFile(old); err != nil || string(got) != "synthetic recoverable image" {
		t.Fatalf("staging recovery changed original: %q err=%v", got, err)
	}
	assertMediaRecoveryRetired(t, store)
}

func TestMediaDeleteRecoveryRejectsMalformedIntentAndChangedCommitMarker(t *testing.T) {
	for _, failure := range []string{"intent", "marker", "missing_schema"} {
		t.Run(failure, func(t *testing.T) {
			store := openTestStore(t)
			sess, old := mediaCrashSession(t, store)
			media := mediaCrashBeforeCommit(t, store, sess, true)
			if failure == "intent" {
				if err := os.WriteFile(media.journal, []byte(fmt.Sprintf(`{"format":1,"operation_id":%q,"original_hash":"../unknown"}`, media.intent.OperationID)), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if failure == "marker" {
				if _, err := store.db.Exec(`INSERT INTO session_media_deletions(operation_id, original_hash, quarantine_name) VALUES(?,?,?)`, media.intent.OperationID, strings.Repeat("b", 32), filepath.Base(media.directory)); err != nil {
					t.Fatal(err)
				}
			} else if _, err := store.db.Exec(`DROP TABLE session_media_deletions`); err != nil {
				t.Fatal(err)
			}
			if err := store.RecoverMediaDeletes(context.Background()); !errors.Is(err, ErrMediaDeleteRecovery) {
				t.Fatalf("corrupt metadata accepted: %v", err)
			}
			if got, err := os.ReadFile(filepath.Join(media.directory, filepath.Base(old))); err != nil || string(got) != "synthetic recoverable image" {
				t.Fatalf("corrupt metadata lost captured bytes: %q err=%v", got, err)
			}
			if _, err := store.Get(sess.ID); err != nil {
				t.Fatalf("corrupt metadata changed SQL conversation: %v", err)
			}
		})
	}
}

func TestMediaDeleteExclusiveRenameCannotReplaceOccupiedEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	from, to := filepath.Join(root, "captured"), filepath.Join(root, "new-original")
	if err := os.Mkdir(from, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(to, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(from, "old"), []byte("captured bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Exercise the atomic syscall itself: the earlier absence check cannot
	// authorize replacing an empty directory published before this rename.
	if err := renameMediaDelete(from, to); err == nil {
		t.Fatal("exclusive rename replaced an occupied directory")
	}
	if got, err := os.ReadFile(filepath.Join(from, "old")); err != nil || string(got) != "captured bytes" {
		t.Fatalf("exclusive rename lost captured bytes: %q err=%v", got, err)
	}
	if entries, err := os.ReadDir(to); err != nil || len(entries) != 0 {
		t.Fatalf("exclusive rename changed the new original: count=%d err=%v", len(entries), err)
	}
}

func TestMediaDeleteRecoveryBoundAndUnknownNamespacePreserveBytes(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		t.Run(fmt.Sprintf("overflow_%v", overflow), func(t *testing.T) {
			store := openTestStore(t)
			namespace := filepath.Join(store.Root(), mediaDeleteNamespace)
			if err := os.Mkdir(namespace, 0o700); err != nil {
				t.Fatal(err)
			}
			unknown := filepath.Join(namespace, "unknown-owned-data")
			if err := os.WriteFile(unknown, []byte("preserved"), 0o600); err != nil {
				t.Fatal(err)
			}
			if overflow {
				for i := 0; i < maxMediaDeleteRecoveryEntries; i++ {
					if err := os.WriteFile(filepath.Join(namespace, fmt.Sprintf("unknown-%04d", i)), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := store.RecoverMediaDeletes(context.Background()); !errors.Is(err, ErrMediaDeleteRecovery) {
				t.Fatalf("unsafe namespace accepted: %v", err)
			}
			if got, err := os.ReadFile(unknown); err != nil || string(got) != "preserved" {
				t.Fatalf("unknown namespace bytes removed: %q err=%v", got, err)
			}
		})
	}
}

func BenchmarkMediaDeleteAbsentLookup(b *testing.B) {
	store := &Store{root: b.TempDir()}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if pending, err := store.HasMediaDeleteRecovery(); err != nil || pending {
			b.Fatalf("absent lookup pending=%v err=%v", pending, err)
		}
	}
}
