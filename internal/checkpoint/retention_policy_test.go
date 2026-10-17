package checkpoint

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionRetentionPolicy(t *testing.T) {
	for _, tc := range []struct {
		name              string
		sessions, running map[string]bool
		nowOffset         time.Duration
		keep, want        int
	}{
		{"orphan pruned", map[string]bool{}, nil, 60 * 24 * time.Hour, 20, 0},
		{"referenced latest kept", map[string]bool{"session": true}, nil, 60 * 24 * time.Hour, 1, 1},
		{"recent orphan kept", map[string]bool{}, nil, 0, 20, 2},
		{"running session kept", map[string]bool{}, map[string]bool{"session": true}, 60 * 24 * time.Hour, 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, data, old, _ := retentionFixture(t)
			retentionTransaction(t, m, func() {
				_, err := PruneSessionsLocked(context.Background(), data, tc.sessions, tc.running, SessionRetention{KeepPerSession: tc.keep, MaxAge: 14 * 24 * time.Hour}, old.CreatedAt.Add(time.Hour+tc.nowOffset))
				if err != nil {
					t.Fatal(err)
				}
				audit, err := AuditStoreRetentionLocked(context.Background(), data)
				if err != nil {
					t.Fatal(err)
				}
				if len(audit.Inventory.Records) != tc.want {
					t.Fatalf("kept=%d want=%d", len(audit.Inventory.Records), tc.want)
				}
				if tc.want == 0 {
					object := filepath.Join(m.repo, "objects", old.Before[:2], old.Before[2:])
					if _, err := os.Stat(object); !os.IsNotExist(err) {
						t.Fatalf("orphan object retained: %v", err)
					}
				}
			})
		})
	}
}

func TestManagedBudgetHonorsSessionKeepPolicy(t *testing.T) {
	m, data, old, newest := retentionFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(data, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sessions(id TEXT PRIMARY KEY); INSERT INTO sessions VALUES('session')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "config.toml"), []byte("[housekeeping]\ncheckpoint_keep_per_session=1\ncheckpoint_max_age_days=14\n"), 0600); err != nil {
		t.Fatal(err)
	}
	retentionTransaction(t, m, func() {
		result, err := EnforceManagedStoreBudgetLocked(context.Background(), data, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range result.Expired {
			if key.ID == newest.ID {
				t.Fatal("managed quota expired retained session checkpoint")
			}
		}
		records := readParallelStoreRecords(t, m)
		if _, ok := records[newest.ID]; !ok {
			t.Fatal("newest checkpoint missing")
		}
		if _, ok := records[old.ID]; ok {
			t.Fatal("old eligible checkpoint was not collected under pressure")
		}
	})
}

func TestSessionRetentionStaleLeaseDoesNotPinHistory(t *testing.T) {
	m, data, old, _ := retentionFixture(t)
	retentionTransaction(t, m, func() {
		dir := filepath.Join(filepath.Dir(m.repo), "leases")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stale.lock"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		n, err := PruneSessionsLocked(context.Background(), data, map[string]bool{}, nil, SessionRetention{KeepPerSession: 1, MaxAge: 24 * time.Hour}, old.CreatedAt.Add(60*24*time.Hour))
		if err != nil || n != 2 {
			t.Fatalf("expired=%d err=%v", n, err)
		}
	})
}

func TestSessionRetentionLeaseKeepsWholeStore(t *testing.T) {
	m, data, _, _ := retentionFixture(t)
	retentionTransaction(t, m, func() {
		dir := filepath.Join(filepath.Dir(m.repo), "leases")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "pending.lock")
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		unlock, err := checkpointStoreLock(path)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		n, err := PruneSessionsLocked(context.Background(), data, map[string]bool{}, nil, SessionRetention{KeepPerSession: 1, MaxAge: 24 * time.Hour}, time.Now().Add(60*24*time.Hour))
		if err != nil || n != 0 {
			t.Fatalf("expired=%d err=%v", n, err)
		}
	})
}
