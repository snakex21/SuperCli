package storage

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Open creates (or opens) the SQLite database inside the data directory
// of the given home. Foreign keys and WAL are enabled because we will
// reach for both as soon as the schema grows.
//
// The returned *sql.DB is safe for concurrent use; the caller is
// responsible for Close().
func Open(home string) (*sql.DB, error) {
	if home == "" {
		return nil, fmt.Errorf("storage.Open: home is empty")
	}
	dir := DataDir(home)
	if err := ensureDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, DBFileName)
	// busy_timeout(5000) makes concurrent writers wait up to 5s
	// for a write lock instead of failing with SQLITE_BUSY. WAL
	// mode (next pragma) means readers do not block writers, so
	// this only matters when multiple writers race.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open(%q): %w", path, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func ensureDir(path string) error {
	if path == "" {
		return fmt.Errorf("ensureDir: empty path")
	}
	// Create the data dir itself (e.g. "<home>/.supercli"), not
	// just its parent. MkdirAll is idempotent and safe to call on
	// an existing directory.
	if err := mkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", path, err)
	}
	return nil
}

const schemaVersion = 1

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("exec %q: %w", s, err)
		}
	}
	var got int
	err := db.QueryRow(`SELECT version FROM schema_version ORDER BY version DESC LIMIT 1`).Scan(&got)
	if err == sql.ErrNoRows {
		_, err = db.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion)
		return err
	}
	if err != nil {
		return err
	}
	if got > schemaVersion {
		return fmt.Errorf("schema version %d is newer than supported %d", got, schemaVersion)
	}
	return nil
}
