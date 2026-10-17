package housekeeping

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"supercli/internal/maintenance"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func TestIdlePassAndRunningSession(t *testing.T) {
	s, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := filepath.Join(s.Root(), ".supercli", "snapshots")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "screen-1.png")
	if err := os.WriteFile(path, []byte("snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	c := config.HousekeepingConfig{}.Defaults()
	end, err := maintenance.BeginPortableRun(context.Background(), s.Root())
	if err != nil {
		t.Fatal(err)
	}
	maintenance.WhilePortableIdle(context.Background(), s.Root(), func(ctx context.Context) { t.Fatal("housekeeping entered a running chat") })
	if _, err := os.Stat(path); err != nil {
		t.Fatal("running chat lost snapshot")
	}
	end()
	ran := maintenance.WhilePortableIdle(context.Background(), s.Root(), func(ctx context.Context) {
		if err := Run(ctx, s, c, time.Now()); err != nil {
			t.Fatal(err)
		}
	})
	if !ran {
		t.Fatal("idle pass skipped")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unreferenced old snapshot not pruned: %v", err)
	}
}

func TestStartDoesNotRunMaintenanceSynchronously(t *testing.T) {
	s, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	Start(s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Root(), ".housekeeping.lock")); !os.IsNotExist(err) {
		t.Fatalf("startup performed maintenance: %v", err)
	}
}
