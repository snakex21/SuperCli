package session

import (
	"context"
	"database/sql"
	"log"
	"path/filepath"
	"time"

	"supercli/internal/system/config"
)

func vacuumDue(c config.HousekeepingConfig, now, last time.Time, pages, free, pageSize int64) bool {
	return c.VacuumIntervalDays > 0 && c.VacuumFreePercent > 0 && c.VacuumMinBytes >= 0 &&
		(last.IsZero() || now.Sub(last) >= days(c.VacuumIntervalDays)) && pages > 0 && free > 0 && pageSize > 0 &&
		free <= pages && float64(free)/float64(pages)*100 >= float64(c.VacuumFreePercent) &&
		float64(free)*float64(pageSize) >= float64(c.VacuumMinBytes)
}

// Called only by the idle, cancellable task. A separate zero-busy-timeout
// connection never waits behind a chat/other process's transaction. Legacy
// databases use VACUUM; new incremental databases reclaim a bounded page batch.
func (s *Store) VacuumIfNeeded(ctx context.Context, c config.HousekeepingConfig, now time.Time) (bool, error) {
	if c.VacuumIntervalDays <= 0 {
		return false, nil
	}
	db, err := sql.Open("sqlite", filepath.Join(s.root, "sessions.db")+"?_pragma=busy_timeout(0)")
	if err != nil {
		return false, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS housekeeping_meta (name TEXT PRIMARY KEY, value INTEGER NOT NULL)`); err != nil {
		return false, err
	}
	var stamp int64
	err = db.QueryRowContext(ctx, `SELECT value FROM housekeeping_meta WHERE name='vacuum'`).Scan(&stamp)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	var last time.Time
	if stamp != 0 {
		last = time.Unix(stamp, 0)
	}
	var pages, free, size, mode int64
	for _, item := range []struct {
		query string
		dest  *int64
	}{{"PRAGMA page_count", &pages}, {"PRAGMA freelist_count", &free}, {"PRAGMA page_size", &size}, {"PRAGMA auto_vacuum", &mode}} {
		if err := db.QueryRowContext(ctx, item.query).Scan(item.dest); err != nil {
			return false, err
		}
	}
	if !vacuumDue(c, now, last, pages, free, size) {
		return false, nil
	}
	command := "VACUUM"
	if mode == 2 {
		command = "PRAGMA incremental_vacuum(16384)"
	}
	started := time.Now()
	if _, err := db.ExecContext(ctx, command); err != nil {
		return false, err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO housekeeping_meta(name,value) VALUES('vacuum',?) ON CONFLICT(name) DO UPDATE SET value=excluded.value`, now.Unix()); err != nil {
		return true, err
	}
	log.Printf("housekeeping: sessions.db %s free_pages=%d page_size=%d elapsed=%s", command, free, size, time.Since(started))
	return true, nil
}
