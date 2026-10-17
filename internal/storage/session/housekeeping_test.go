package session

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"supercli/internal/system/config"
)

func TestHousekeepingVacuumConditions(t *testing.T) {
	now := time.Now()
	c := config.HousekeepingConfig{}.Defaults()
	for _, tc := range []struct {
		name              string
		last              time.Time
		pages, free, size int64
		want              bool
	}{
		{"due", time.Time{}, 100000, 30000, 4096, true},
		{"recent", now.Add(-24 * time.Hour), 100000, 30000, 4096, false},
		{"ratio", time.Time{}, 100000, 20000, 4096, false},
		{"small", time.Time{}, 100, 50, 4096, false},
		{"empty", time.Time{}, 0, 0, 4096, false},
		{"future clock", now.Add(time.Hour), 100000, 30000, 4096, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := vacuumDue(c, now, tc.last, tc.pages, tc.free, tc.size); got != tc.want {
				t.Fatalf("due=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestHousekeepingVacuumLegacyAndIncremental(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "incremental", true: "legacy"}[legacy], func(t *testing.T) {
			root := t.TempDir()
			if legacy {
				db, err := sql.Open("sqlite", filepath.Join(root, "sessions.db"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`CREATE TABLE legacy_seed(id INTEGER)`); err != nil {
					t.Fatal(err)
				}
				db.Close()
			}
			s, err := OpenStore(root)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if _, err := s.db.Exec(`CREATE TABLE vacuum_filler(data BLOB); INSERT INTO vacuum_filler VALUES(zeroblob(2097152)); DELETE FROM vacuum_filler`); err != nil {
				t.Fatal(err)
			}
			before := pragmaInt(t, s.db, "freelist_count")
			if before == 0 {
				t.Fatal("fixture did not free pages")
			}
			c := config.HousekeepingConfig{}.Defaults()
			c.VacuumMinBytes = 1
			c.VacuumFreePercent = 1
			ran, err := s.VacuumIfNeeded(context.Background(), c, time.Now())
			if err != nil || !ran {
				t.Fatalf("ran=%v err=%v", ran, err)
			}
			after := pragmaInt(t, s.db, "freelist_count")
			if after >= before {
				t.Fatalf("freelist did not shrink: %d -> %d", before, after)
			}
			ran, err = s.VacuumIfNeeded(context.Background(), c, time.Now())
			if err != nil || ran {
				t.Fatalf("repeat ran=%v err=%v", ran, err)
			}
		})
	}
}

func TestHousekeepingSnapshotsReferencesAgeAndSize(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sess, err := s.Create(t.TempDir(), "echo", "snapshots")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.root, ".supercli", "snapshots")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	files := []struct {
		name string
		age  time.Duration
		kept bool
	}{
		{"screen-1.png", 60 * 24 * time.Hour, false},
		{"screen-2.png", 60 * 24 * time.Hour, true},
		{"screen-3.png", time.Hour, true},
		{"screen-4.png", 10 * 24 * time.Hour, false},
		{"screen-5.png", 60 * 24 * time.Hour, true},
		{"custom.png", 60 * 24 * time.Hour, true},
		{"screen-6.png", 60 * 24 * time.Hour, true},
	}
	for _, f := range files {
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, []byte("image"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, now.Add(-f.age), now.Add(-f.age)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO messages(session_id,seq,role,content,created_at) VALUES(?,1,'tool',?,?)`, sess.ID, `{"path":"snapshot:screen-2.png"}`, now.UnixNano()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO tool_outputs(handle,session_id,content,bytes) VALUES('snapshot',?,?,1)`, sess.ID, []byte(`{"preview_path":"C:\\data\\screen-5.png"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO prompt_queue(id,cwd,prompt,position,created_at) VALUES('queue',?,'snapshot:screen-6.png',1,?)`, s.root, now.UnixNano()); err != nil {
		t.Fatal(err)
	}
	c := config.HousekeepingConfig{}.Defaults()
	c.SnapshotMaxBytes = 1
	removed, err := s.PruneSnapshotFiles(context.Background(), c, now)
	if err != nil || removed != 2 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	for _, f := range files {
		_, err := os.Stat(filepath.Join(dir, f.name))
		if f.kept && err != nil || !f.kept && !os.IsNotExist(err) {
			t.Errorf("%s kept=%v err=%v", f.name, f.kept, err)
		}
	}
}

func TestHousekeepingSnapshotFailedCensusDeletesNothing(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := filepath.Join(s.root, ".supercli", "snapshots")
	os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, "screen-1.png")
	os.WriteFile(path, []byte("data"), 0600)
	old := time.Now().Add(-90 * 24 * time.Hour)
	os.Chtimes(path, old, old)
	if _, err := s.db.Exec(`DROP TABLE tool_outputs`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneSnapshotFiles(context.Background(), config.HousekeepingConfig{}.Defaults(), time.Now()); err == nil {
		t.Fatal("expected census error")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("incomplete census deleted snapshot")
	}
}
