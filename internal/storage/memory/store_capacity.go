package memory

import (
	"database/sql"
	"errors"
	"fmt"
)

// Store usage is durable and updated by SQLite triggers, including writes from
// another Store/process. Creating it under an IMMEDIATE transaction makes the
// initial total and subsequent mutations one consistent sequence.
func (s *Store) migrateCapacity() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS memory_store_usage (
 id INTEGER PRIMARY KEY CHECK(id=1),
 entries INTEGER NOT NULL CHECK(entries>=0),
 content_bytes INTEGER NOT NULL CHECK(content_bytes>=0)
 )`); err != nil {
		return err
	}
	var exists int
	err = tx.QueryRow(`SELECT 1 FROM memory_store_usage WHERE id=1`).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.Exec(`INSERT INTO memory_store_usage(id,entries,content_bytes)
  SELECT 1,COUNT(*),COALESCE(SUM(length(CAST(content AS BLOB))),0) FROM memory_entries`)
	}
	if err != nil {
		return err
	}
	for _, statement := range []string{
		`CREATE TRIGGER IF NOT EXISTS memory_usage_insert AFTER INSERT ON memory_entries BEGIN
 UPDATE memory_store_usage SET entries=entries+1,content_bytes=content_bytes+length(CAST(new.content AS BLOB)) WHERE id=1;
 END`,
		`CREATE TRIGGER IF NOT EXISTS memory_usage_delete AFTER DELETE ON memory_entries BEGIN
 UPDATE memory_store_usage SET entries=entries-1,content_bytes=content_bytes-length(CAST(old.content AS BLOB)) WHERE id=1;
 END`,
		`CREATE TRIGGER IF NOT EXISTS memory_usage_content AFTER UPDATE OF content ON memory_entries BEGIN
 UPDATE memory_store_usage SET content_bytes=content_bytes-length(CAST(old.content AS BLOB))+length(CAST(new.content AS BLOB)) WHERE id=1;
 END`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type memoryCapacityReader interface {
	QueryRow(query string, args ...any) *sql.Row
}

// Check within the Put transaction so independent writers cannot both consume
// the last free slot. Updating an existing ID discounts its committed old body.
func checkMemoryCapacity(reader memoryCapacityReader, e Entry) (string, error) {
	var entries, contentBytes int64
	var previousScope string
	err := reader.QueryRow(`SELECT u.entries-CASE WHEN e.id IS NULL THEN 0 ELSE 1 END,
 u.content_bytes-COALESCE(length(CAST(e.content AS BLOB)),0),COALESCE(e.scope,'')
 FROM memory_store_usage u LEFT JOIN memory_entries e ON e.id=? WHERE u.id=1`, e.ID).Scan(&entries, &contentBytes, &previousScope)
	if err != nil {
		return "", fmt.Errorf("memory.Store.Put(%s): capacity check: %w", e.ID, err)
	}
	if entries+1 > MaxStoreEntries {
		return "", fmt.Errorf("memory.Store.Put(%s): store entry limit %d reached; delete or compact old memories before saving more", e.ID, MaxStoreEntries)
	}
	if contentBytes+int64(len(e.Content)) > MaxStoreContentBytes {
		return "", fmt.Errorf("memory.Store.Put(%s): store content limit %d bytes reached; delete or compact old memories before saving more", e.ID, MaxStoreContentBytes)
	}
	return previousScope, nil
}
