package app

import (
	"context"
	"errors"
	"os"
	"testing"

	"supercli/internal/checkpoint"
	"supercli/internal/storage/session"
)

func TestSessionStackRecoversCommittedDeleteBeforeOpeningLiveWriter(t *testing.T) {
	data, home := t.TempDir(), t.TempDir()
	store, err := session.OpenStore(data)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.EnsureSession("old", home, "model"); err != nil {
		t.Fatal(err)
	}
	old, err := session.NewWriter(store, "old").ExternalizeImage(ctx, "image/png", []byte("captured old image"))
	if err != nil {
		t.Fatal(err)
	}
	gate, err := checkpoint.NewStoreGate(data)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := gate.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, deleteErr := store.DeleteRows(ctx, "old") // Deliberately omit post-commit cleanup.
	if err := errors.Join(deleteErr, guard.Close()); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSession("old", home, "replacement"); err != nil {
		t.Fatal(err)
	}
	fresh, err := session.NewWriter(store, "old").ExternalizeImage(ctx, "image/png", []byte("fresh image"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, writer := openSessionStack(data, "current", home, "model", nil)
	if reopened == nil || writer == nil {
		t.Fatal("startup recovery disabled persistence")
	}
	defer reopened.Close()
	if pending, err := reopened.HasMediaDeleteRecovery(); err != nil || pending {
		t.Fatalf("startup retained committed intent: %v %v", pending, err)
	}
	if _, err := os.Stat(old.Path); !os.IsNotExist(err) {
		t.Fatalf("old captured media not deleted: %v", err)
	}
	if bytes, err := os.ReadFile(fresh.Path); err != nil || string(bytes) != "fresh image" {
		t.Fatalf("startup recovery touched replacement: %q %v", bytes, err)
	}
	if _, err := reopened.Get("current"); err != nil {
		t.Fatalf("live session not established: %v", err)
	}
}

func TestSessionStartupAbsentDeleteIntentNeedsNoSQL(t *testing.T) {
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := recoverSessionMediaDeletes(store, store.Root()); err != nil {
		t.Fatalf("absent startup recovery touched closed DB: %v", err)
	}
}
