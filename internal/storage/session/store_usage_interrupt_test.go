package session

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestTryUpdateUsageAddsEachObservationExactlyOnce(t *testing.T) {
	s := openTestStore(t)
	first := mustCreateUsageSession(t, s, "/first")
	second := mustCreateUsageSession(t, s, "/second")
	if err := s.UpdateUsage(first.ID, 10, 5); err != nil {
		t.Fatal(err)
	}
	for _, delta := range [][2]int{{20, 3}, {2, 7}} {
		if err := s.TryUpdateUsage(context.Background(), first.ID, delta[0], delta[1]); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Get(first.ID)
	if err != nil || got.TokenIn != 32 || got.TokenOut != 15 || got.MessageCount != 0 {
		t.Fatalf("usage changed more than once or added history: %+v, err=%v", got, err)
	}
	other, err := s.Get(second.ID)
	if err != nil || other.TokenIn != 0 || other.TokenOut != 0 {
		t.Fatalf("other session changed: %+v, err=%v", other, err)
	}
	if err := s.TryUpdateUsage(context.Background(), "absent-session", 1, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing session error=%v", err)
	}
}

func TestTryUpdateUsageCanceledOrExpiredDoesNotChangeCounters(t *testing.T) {
	s := openTestStore(t)
	sess := mustCreateUsageSession(t, s, "/cancel")
	if err := s.UpdateUsage(sess.ID, 10, 5); err != nil {
		t.Fatal(err)
	}
	before, err := s.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"canceled", canceled, context.Canceled},
		{"expired", expired, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.TryUpdateUsage(tc.ctx, sess.ID, 20, 3); !errors.Is(err, tc.want) {
				t.Fatalf("context error=%v, want %v", err, tc.want)
			}
			got, err := s.Get(sess.ID)
			if err != nil || got.TokenIn != before.TokenIn || got.TokenOut != before.TokenOut || !got.UpdatedAt.Equal(before.UpdatedAt) {
				t.Fatalf("canceled write changed session: %+v, err=%v", got, err)
			}
		})
	}
}

func TestTryUpdateUsageWriterContentionFailsBeforeDeadline(t *testing.T) {
	for _, sharedBlocker := range []bool{false, true} {
		name := "independent-pool"
		if sharedBlocker {
			name = "borrowed-shared-handle"
		}
		t.Run(name, func(t *testing.T) {
			s := openTestStore(t)
			sess := mustCreateUsageSession(t, s, "/busy")
			if err := s.UpdateUsage(sess.ID, 10, 5); err != nil {
				t.Fatal(err)
			}
			blocker := s.db
			if !sharedBlocker {
				var err error
				blocker, err = sql.Open("sqlite", filepath.Join(s.Root(), "sessions.db")+"?_pragma=busy_timeout(0)")
				if err != nil {
					t.Fatal(err)
				}
				defer blocker.Close()
			}
			held, err := blocker.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if _, err := held.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
				t.Fatal(err)
			}
			defer held.ExecContext(context.Background(), "ROLLBACK")
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			started := time.Now()
			err = s.TryUpdateUsage(ctx, sess.ID, 20, 3)
			elapsed := time.Since(started)
			if err == nil {
				t.Fatal("locked writer accepted interrupted usage")
			}
			// A Context UPDATE on the ordinary pool used to wait busy_timeout(5000).
			// The private connection must fail while this short deadline is live.
			if elapsed >= time.Second || ctx.Err() != nil {
				t.Fatalf("busy write exceeded relative deadline: elapsed=%v ctx=%v err=%v", elapsed, ctx.Err(), err)
			}
			if _, err := held.ExecContext(context.Background(), "ROLLBACK"); err != nil {
				t.Fatal(err)
			}
			if sharedBlocker {
				var timeout int
				if err := held.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
					t.Fatalf("borrowed shared busy_timeout=%d err=%v", timeout, err)
				}
			}
			var timeout int
			if err := s.db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
				t.Fatalf("shared pool busy_timeout=%d err=%v", timeout, err)
			}
			got, err := s.Get(sess.ID)
			if err != nil || got.TokenIn != 10 || got.TokenOut != 5 {
				t.Fatalf("contended write changed counters: %+v err=%v", got, err)
			}
			if err := s.TryUpdateUsage(context.Background(), sess.ID, 20, 3); err != nil {
				t.Fatalf("uncontended write failed: %v", err)
			}
			got, err = s.Get(sess.ID)
			if err != nil || got.TokenIn != 30 || got.TokenOut != 8 {
				t.Fatalf("retry duplicated failed write: %+v err=%v", got, err)
			}
		})
	}
}

func TestTryUpdateUsageRejectsUnavailableStore(t *testing.T) {
	s := openTestStore(t)
	sess := mustCreateUsageSession(t, s, "/closed")
	if err := s.TryUpdateUsage(context.Background(), "", 1, 1); err == nil {
		t.Fatal("empty sessionID accepted")
	}
	if err := (*Store)(nil).TryUpdateUsage(context.Background(), sess.ID, 1, 1); err == nil {
		t.Fatal("nil Store accepted")
	}
	if err := (&Store{}).TryUpdateUsage(context.Background(), sess.ID, 1, 1); err == nil {
		t.Fatal("unopened Store accepted")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.TryUpdateUsage(context.Background(), sess.ID, 1, 1); err == nil {
		t.Fatal("closed Store was reopened")
	}
	reopened, err := OpenStore(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Get(sess.ID)
	if err != nil || got.TokenIn != 0 || got.TokenOut != 0 {
		t.Fatalf("closed Store changed durable counters: %+v err=%v", got, err)
	}
}
