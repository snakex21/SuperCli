package session

import (
	"context"
	"time"
)

// sessionJournalSizeLimit bounds the WAL file that remains on disk after a
// checkpoint resets it (SQLite truncates it to at most this size).
const sessionJournalSizeLimit = 32 << 20 // 32 MiB

// sessionDSNPragmas apply to every pooled connection, in order. Only
// connection-local settings belong here: PRAGMA auto_vacuum=INCREMENTAL on an
// already-incremental file rewrites header meta[6] and therefore needs the
// write lock on every new connection (it would wait busy_timeout behind any
// other writer, e.g. the GUI and TUI sharing the file).
const sessionDSNPragmas = "?_pragma=busy_timeout(5000)" +
	"&_pragma=journal_mode(WAL)" +
	"&_pragma=journal_size_limit(33554432)" +
	"&_pragma=foreign_keys(1)"

// initAutoVacuum enables incremental auto_vacuum on a brand-new, still empty
// database, before migrate() creates the first table. Setting the pragma after
// journal_mode(WAL) has initialised page 1 is only applied by a VACUUM, which
// is instant on an empty file. Existing databases (any schema object present)
// are never touched here, so legacy auto_vacuum=NONE files keep their mode and
// startup never performs a large VACUUM. Best effort: if another process wins
// the race and creates the schema first, the file simply stays as it is.
func (s *Store) initAutoVacuum() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return
	}
	defer conn.Close()
	var objects, mode int
	if conn.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master`).Scan(&objects) != nil || objects != 0 {
		return
	}
	if conn.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode) != nil || mode != 0 {
		return
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
		return
	}
	// Re-check on the same connection: never VACUUM a file another process
	// has populated in the meantime (that would be a long, locking rewrite).
	if conn.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master`).Scan(&objects) != nil || objects != 0 {
		return
	}
	_, _ = conn.ExecContext(ctx, `VACUUM`)
}

// reclaimFreePages returns pages freed by an already committed deletion to
// the filesystem. It is a no-op for legacy auto_vacuum=NONE databases. Failure
// (for example SQLITE_BUSY from another process) never changes the outcome of
// the committed deletion; the free pages simply remain reusable by SQLite and
// are reclaimed by the next successful run.
func (s *Store) reclaimFreePages() {
	if s == nil || s.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = s.db.ExecContext(ctx, `PRAGMA incremental_vacuum`)
}
