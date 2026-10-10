package webgui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"supercli/internal/checkpoint"
	"supercli/internal/storage/session"
)

func TestEngineStartupRecoversCommittedMediaDeleteAndCachesOpenedStore(t *testing.T) {
	home, dataDir := t.TempDir(), t.TempDir()
	old, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = old.Close() })
	store, err := old.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(home, "echo", "synthetic pending media delete")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	image, err := session.NewWriter(store, sess.ID).ExternalizeImage(ctx, "image/png", []byte("captured image"))
	if err != nil {
		t.Fatal(err)
	}
	gate, err := checkpoint.NewStoreGate(dataDir)
	if errors.Is(err, checkpoint.ErrStoreUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := gate.Acquire(ctx)
	if errors.Is(err, checkpoint.ErrStoreUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	_, deleteErr := store.DeleteRows(ctx, sess.ID) // crash before callback
	if err := errors.Join(deleteErr, transaction.Close()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(image.Path); !os.IsNotExist(err) {
		t.Fatalf("fixture did not detach old image: %v", err)
	}
	if err := store.EnsureSession(sess.ID, home, "replacement"); err != nil {
		t.Fatal(err)
	}
	fresh, err := session.NewWriter(store, sess.ID).ExternalizeImage(ctx, "image/png", []byte("fresh image"))
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Close() })
	opened, err := current.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	if pending, err := opened.HasMediaDeleteRecovery(); err != nil || pending {
		t.Fatalf("startup did not retire committed recovery: %v err=%v", pending, err)
	}
	if got, err := os.ReadFile(fresh.Path); err != nil || string(got) != "fresh image" {
		t.Fatalf("startup deleted fresh original: %q err=%v", got, err)
	}
	// An already cached Store has no per-turn scan, even if another actor
	// creates a new pending namespace. Explicit delete/reopen does recovery.
	namespace := filepath.Join(dataDir, ".session-media-delete")
	if err := os.Mkdir(namespace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(namespace, "unknown"), []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	if again, err := current.sessionStore(); err != nil || again != opened {
		t.Fatalf("cached store unexpectedly rescanned/reopened: same=%v err=%v", again == opened, err)
	}
}
