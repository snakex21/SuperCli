package housekeeping

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"time"

	"supercli/internal/checkpoint"
	"supercli/internal/maintenance"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

// Start only a timer at open: no census, Git, hashing or VACUUM on startup.
// The store owns the task and cancels/joins it before closing SQLite.
func Start(s *session.Store) {
	s.StartBackgroundTask(func(ctx context.Context) {
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			completed := false
			maintenance.WhilePortableIdle(ctx, s.Root(), func(idle context.Context) {
				bounded, stop := context.WithTimeout(idle, 2*time.Minute)
				defer stop()
				tc, err := config.LoadToml(filepath.Join(s.Root(), "config.toml"))
				if err != nil {
					log.Printf("housekeeping: config: %v", err)
					return
				}
				c := tc.Housekeeping.Defaults()
				if c.Enabled != nil && !*c.Enabled {
					return
				}
				if err := Run(bounded, s, c, time.Now()); err != nil {
					if !errors.Is(err, context.Canceled) {
						log.Printf("housekeeping: %v", err)
					}
					return
				}
				completed = true
			})
			delay := time.Hour
			if completed {
				delay = 24 * time.Hour
			}
			timer.Reset(delay)
		}
	})
}

// Run executes one conservative pass, only while the caller owns idle priority.
func Run(ctx context.Context, s *session.Store, c config.HousekeepingConfig, now time.Time) error {
	gate, err := checkpoint.NewStoreGate(s.Root())
	if err != nil {
		return err
	}
	lease, err := gate.TryAcquire(ctx)
	if errors.Is(err, checkpoint.ErrStoreBusy) {
		return nil
	}
	if err != nil {
		return err
	}
	defer lease.Close()
	// A complete ID census is mandatory; never infer orphanhood from a failed
	// lookup or a paginated list. StoreGate also serializes session deletion.
	sessions, err := s.SessionIDs(ctx)
	if err != nil {
		return err
	}
	if c.CheckpointKeepPerSession >= 0 && c.CheckpointMaxAgeDays > 0 {
		if _, err := checkpoint.PruneSessionsLocked(ctx, s.Root(), sessions, nil, checkpoint.SessionRetention{KeepPerSession: c.CheckpointKeepPerSession, MaxAge: days(c.CheckpointMaxAgeDays)}, now); err != nil {
			return err
		}
	}
	if c.SnapshotMaxAgeDays > 0 && c.SnapshotMaxBytes >= 0 {
		if _, err := s.PruneSnapshotFiles(ctx, c, now); err != nil {
			return err
		}
	}
	_, err = s.VacuumIfNeeded(ctx, c, now)
	return err
}

// Clamp untrusted config before duration multiplication.
func days(n int) time.Duration {
	if n > 36500 {
		n = 36500
	}
	return time.Duration(n) * 24 * time.Hour
}
