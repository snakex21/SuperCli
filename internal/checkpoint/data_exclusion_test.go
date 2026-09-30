package checkpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeCheckpointFixture(t *testing.T, p, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestNestedApplicationDataIsNotCapturedOrRewound(t *testing.T) {
	for _, name := range []string{"portable-data", "run state[1]"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			home := t.TempDir()
			data := filepath.Join(home, name)
			app := filepath.Join(home, "app.txt")
			cache := filepath.Join(data, "prefill-profiles.json")
			writeCheckpointFixture(t, app, "before")
			writeCheckpointFixture(t, cache, "cache-before")
			writeCheckpointFixture(t, filepath.Join(data, "skills", "pack.bin"), strings.Repeat("private fixture", 4096))
			// Identically named source directories outside the actual data root remain visible.
			sibling := filepath.ToSlash(filepath.Join("examples", name, "doc.txt"))
			writeCheckpointFixture(t, filepath.Join(home, filepath.FromSlash(sibling)), "source fixture")
			m, err := Open(home, data)
			if errors.Is(err, ErrUnavailable) {
				t.Skip(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			turn := m.NewTurn("s", "edit source")
			if err := turn.ensureBefore(ctx); err != nil {
				t.Fatal(err)
			}
			paths, err := m.git(ctx, "ls-tree", "-r", "--name-only", turn.before)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"app.txt", sibling}
			if got := strings.Split(strings.TrimSpace(paths), "\n"); !reflect.DeepEqual(got, want) {
				t.Fatalf("snapshot includes app data or excludes source: %q, want %q", got, want)
			}
			writeCheckpointFixture(t, app, "after")
			writeCheckpointFixture(t, cache, "cache-current")
			rec, err := turn.Complete(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if rec == nil || !reflect.DeepEqual(rec.Files, []string{"app.txt"}) {
				t.Fatalf("file changes=%+v", rec)
			}
			if _, err := m.Undo(ctx, rec.ID); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(cache); string(got) != "cache-current" {
				t.Fatalf("undo rewound app data: %q", got)
			}
			if _, err := m.Redo(ctx, rec.ID); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(cache); string(got) != "cache-current" {
				t.Fatalf("redo rewound app data: %q", got)
			}
		})
	}
}

func TestReopenedCheckpointUpgradesLegacyDataExclusions(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	data := filepath.Join(home, "portable-data")
	app := filepath.Join(home, "app.txt")
	cache := filepath.Join(data, "prefill-profiles.json")
	writeCheckpointFixture(t, app, "before")
	writeCheckpointFixture(t, cache, "cache-before")
	opened, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the old checkpoint index and stored records without changing user data.
	legacy := &Manager{home: opened.home, repo: opened.repo, meta: opened.meta, excludes: ".git/\n.supercli/\ncheckpoints/\nsessions.db*\n*.db-wal\n*.db-shm\nportable-data/checkpoints/\n"}
	turn := legacy.NewTurn("s", "old source edit")
	if err := turn.ensureBefore(ctx); err != nil {
		t.Fatal(err)
	}
	writeCheckpointFixture(t, app, "after")
	writeCheckpointFixture(t, cache, "cache-after")
	old, err := turn.Complete(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if old == nil || len(old.Files) != 2 {
		t.Fatalf("legacy fixture=%+v", old)
	}
	writeCheckpointFixture(t, cache, "current-data")
	reopened, err := Open(home, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Undo(ctx, old.ID); err != nil {
		t.Fatalf("source undo must not conflict with application data: %v", err)
	}
	if got, _ := os.ReadFile(cache); string(got) != "current-data" {
		t.Fatalf("old undo rewound app data: %q", got)
	}
	if got, _ := os.ReadFile(app); string(got) != "before" {
		t.Fatalf("old source undo failed: %q", got)
	}
	if _, err := reopened.Redo(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	next := reopened.NewTurn("s", "next edit")
	if err := next.ensureBefore(ctx); err != nil {
		t.Fatal(err)
	}
	paths, err := reopened.git(ctx, "ls-tree", "-r", "--name-only", next.before)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(paths) != "app.txt" {
		t.Fatalf("legacy index still captures app data: %s", paths)
	}
}

func TestApplicationDataOutsideWorkspaceDoesNotExcludeSource(t *testing.T) {
	ctx := context.Background()
	home, data := t.TempDir(), t.TempDir()
	writeCheckpointFixture(t, filepath.Join(home, filepath.Base(data), "source.go"), "source")
	m, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	turn := m.NewTurn("s", "edit")
	if err := turn.ensureBefore(ctx); err != nil {
		t.Fatal(err)
	}
	paths, err := m.git(ctx, "ls-tree", "-r", "--name-only", turn.before)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(paths, "source.go") {
		t.Fatalf("external data directory hid project source: %q", paths)
	}
}
