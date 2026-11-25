package session

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"
)

// Database slimming (after 1.0.40). An audited sessions.db of 540 MB held
// 35 MB of worker tool snapshots inline in messages.origin_json (up to
// 274 KB per notification row) and 172 MB of finished workers' full
// histories. Snapshots are now stored once per conversation in tool_outputs
// under a "worker_act_" handle (never evicted, deleted with the session) and
// read back transparently; finished workers' histories are pruned on a safe
// policy; existing rows are migrated once, after a backup copy.

const (
	// workerActivityInlineMax: larger worker snapshots leave origin_json.
	workerActivityInlineMax = 4096
	// WorkerActivityHandlePrefix names externalized snapshots in tool_outputs.
	WorkerActivityHandlePrefix = "worker_act_"
	workerActivityRefKey       = "worker_activity_ref"
)

type sqlExecQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// externalizeWorkerActivity moves a large worker_activity payload out of an
// origin JSON into tool_outputs and returns the origin with a reference.
func externalizeWorkerActivity(ctx context.Context, q sqlExecQuerier, sessionID, origin string) (string, error) {
	if len(origin) <= workerActivityInlineMax || !strings.Contains(origin, `"worker_activity"`) {
		return origin, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(origin), &fields) != nil {
		return origin, nil
	}
	activity := fields["worker_activity"]
	if len(activity) <= workerActivityInlineMax {
		return origin, nil
	}
	sum := sha256.Sum256(append([]byte(sessionID+"\x00"), activity...))
	handle := WorkerActivityHandlePrefix + hex.EncodeToString(sum[:16])
	if _, err := q.ExecContext(ctx, `INSERT INTO tool_outputs(handle,session_id,content,bytes)
  SELECT ?,?,CAST(? AS BLOB),? WHERE NOT EXISTS (SELECT 1 FROM tool_outputs WHERE handle = ?)`,
		handle, sessionID, string(activity), len(activity), handle); err != nil {
		return origin, fmt.Errorf("externalize worker activity: %w", err)
	}
	delete(fields, "worker_activity")
	ref, _ := json.Marshal(handle)
	fields[workerActivityRefKey] = ref
	out, err := json.Marshal(fields)
	if err != nil {
		return origin, err
	}
	return string(out), nil
}

// rehydrateWorkerActivity puts externalized snapshots back into the origin
// JSON of transcript rows, so every reader sees what was originally stored.
func (s *Store) rehydrateWorkerActivity(ctx context.Context, rows []Encoded) {
	for i := range rows {
		if !strings.Contains(rows[i].OriginJSON, workerActivityRefKey) {
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(rows[i].OriginJSON), &fields) != nil {
			continue
		}
		var handle string
		if json.Unmarshal(fields[workerActivityRefKey], &handle) != nil || !strings.HasPrefix(handle, WorkerActivityHandlePrefix) {
			continue
		}
		var content string
		if err := s.db.QueryRowContext(ctx, "SELECT CAST(content AS TEXT) FROM tool_outputs WHERE handle = ?", handle).Scan(&content); err != nil {
			continue // a missing snapshot only loses the UI detail, never the row
		}
		delete(fields, workerActivityRefKey)
		fields["worker_activity"] = json.RawMessage(content)
		if out, err := json.Marshal(fields); err == nil {
			rows[i].OriginJSON = string(out)
		}
	}
}

// SlimPolicy bounds the finished-worker history prune.
type SlimPolicy struct {
	// WorkerMaxAge: finished ("done") workers last updated longer ago are
	// pruned. Failed, stopped, interrupted (resumable) and running workers
	// never are.
	WorkerMaxAge time.Duration
	// WorkerKeepLast: messages kept from the end of a pruned history, besides
	// the brief (first user message) and the final report.
	WorkerKeepLast int
}

// DefaultSlimPolicy keeps two weeks of full finished-worker histories.
var DefaultSlimPolicy = SlimPolicy{WorkerMaxAge: 14 * 24 * time.Hour, WorkerKeepLast: 24}

// SlimReport says what a slimming pass did.
type SlimReport struct {
	Backup           string
	ActivityRows     int
	PrunedWorkers    int
	BytesBefore      int64
	BytesAfter       int64
	Elapsed          time.Duration
	MigrationApplied bool
}

const slimMigrationKey = "slim_v1"

// Slim runs the one-time migration of inline worker snapshots (after a backup
// copy of the database next to it) and the finished-worker history prune.
// Both are idempotent and work in small transactions, streaming row ids.
func (s *Store) Slim(ctx context.Context, policy SlimPolicy, now time.Time) (SlimReport, error) {
	var rep SlimReport
	start := time.Now()
	rep.BytesBefore = s.fileBytes(ctx)
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS housekeeping_meta (name TEXT PRIMARY KEY, value INTEGER NOT NULL)`); err != nil {
		return rep, err
	}
	var done int64
	_ = s.db.QueryRowContext(ctx, `SELECT value FROM housekeeping_meta WHERE name = ?`, slimMigrationKey).Scan(&done)
	cutoff := now.Add(-policy.WorkerMaxAge).UnixMilli()
	var inline, prunable int
	if done == 0 {
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE length(origin_json) > ? AND instr(origin_json, '"worker_activity"') > 0`, workerActivityInlineMax).Scan(&inline)
	}
	if policy.WorkerMaxAge > 0 {
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_workers WHERE status = 'done' AND updated_at < ?`, cutoff).Scan(&prunable)
	}
	if done == 0 && (inline > 0 || prunable > 0) {
		// A consistent copy of the whole database before the first rewrite.
		rep.Backup = filepath.Join(s.root, "sessions.db.bak-"+now.Format("20060102-150405"))
		if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, rep.Backup); err != nil {
			return rep, fmt.Errorf("slim backup: %w", err)
		}
	}
	if done == 0 && inline > 0 {
		n, err := s.migrateInlineWorkerActivity(ctx)
		rep.ActivityRows = n
		if err != nil {
			return rep, err
		}
	}
	if prunable > 0 {
		n, err := s.pruneFinishedWorkers(ctx, cutoff, policy.WorkerKeepLast)
		rep.PrunedWorkers = n
		if err != nil {
			return rep, err
		}
	}
	if done == 0 {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO housekeeping_meta(name,value) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value`, slimMigrationKey, now.Unix()); err != nil {
			return rep, err
		}
		rep.MigrationApplied = true
		if rep.ActivityRows > 0 || rep.PrunedWorkers > 0 {
			// Give the freed pages back to the file system once.
			if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
				log.Printf("slim: vacuum: %v", err)
			}
		}
	}
	rep.BytesAfter = s.fileBytes(ctx)
	rep.Elapsed = time.Since(start)
	return rep, nil
}

func (s *Store) fileBytes(ctx context.Context) int64 {
	var pages, size int64
	_ = s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages)
	_ = s.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&size)
	return pages * size
}

// migrateInlineWorkerActivity streams the rows by id in small transactions.
func (s *Store) migrateInlineWorkerActivity(ctx context.Context) (int, error) {
	moved, after := 0, int64(0)
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM messages WHERE id > ? AND length(origin_json) > ? AND instr(origin_json, '"worker_activity"') > 0 ORDER BY id LIMIT 64`, after, workerActivityInlineMax)
		if err != nil {
			return moved, err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return moved, err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if len(ids) == 0 {
			return moved, nil
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return moved, err
		}
		for _, id := range ids {
			var sessionID, origin string
			if err := tx.QueryRowContext(ctx, `SELECT session_id, origin_json FROM messages WHERE id = ?`, id).Scan(&sessionID, &origin); err != nil {
				tx.Rollback()
				return moved, err
			}
			slim, err := externalizeWorkerActivity(ctx, tx, sessionID, origin)
			if err != nil {
				tx.Rollback()
				return moved, err
			}
			if slim != origin {
				if _, err := tx.ExecContext(ctx, `UPDATE messages SET origin_json = ? WHERE id = ?`, slim, id); err != nil {
					tx.Rollback()
					return moved, err
				}
				moved++
			}
		}
		if err := tx.Commit(); err != nil {
			return moved, err
		}
		after = ids[len(ids)-1]
	}
}

// pruneFinishedWorkers keeps the brief, the final report and the last keep
// messages of finished workers older than cutoff (ms), with a marker.
func (s *Store) pruneFinishedWorkers(ctx context.Context, cutoff int64, keep int) (int, error) {
	if keep < 4 {
		keep = 4
	}
	rows, err := s.db.QueryContext(ctx, `SELECT session_id, worker_id FROM session_workers WHERE status = 'done' AND updated_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	type key struct{ session, worker string }
	var keys []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.session, &k.worker); err != nil {
			rows.Close()
			return 0, err
		}
		keys = append(keys, k)
	}
	rows.Close()
	pruned := 0
	for _, k := range keys {
		var raw []byte
		if err := s.db.QueryRowContext(ctx, `SELECT messages_json FROM session_workers WHERE session_id = ? AND worker_id = ? AND status = 'done'`, k.session, k.worker).Scan(&raw); err != nil {
			continue
		}
		slim, ok := pruneWorkerHistory(raw, keep)
		if !ok {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE session_workers SET messages_json = ? WHERE session_id = ? AND worker_id = ? AND status = 'done'`, slim, k.session, k.worker); err != nil {
			return pruned, err
		}
		pruned++
	}
	return pruned, nil
}

// pruneWorkerHistory keeps the first user message (the brief), the last keep
// messages (which end with the final report) and a marker between them.
func pruneWorkerHistory(raw []byte, keep int) ([]byte, bool) {
	var msgs []map[string]json.RawMessage
	if json.Unmarshal(raw, &msgs) != nil || len(msgs) <= keep+2 {
		return raw, false
	}
	brief := -1
	for i, m := range msgs {
		var role string
		_ = json.Unmarshal(m["Role"], &role)
		if role == "user" {
			brief = i
			break
		}
	}
	tail := len(msgs) - keep
	// Never start the kept tail with orphan tool results.
	for tail < len(msgs) {
		var role string
		_ = json.Unmarshal(msgs[tail]["Role"], &role)
		if role != "tool" {
			break
		}
		tail++
	}
	if brief < 0 || brief >= tail {
		return raw, false
	}
	marker, _ := json.Marshal(map[string]any{"Role": "user", "Content": fmt.Sprintf("[%d older messages of this finished worker were pruned to save space; its brief and final messages are kept]", tail-brief-1)})
	out := append([]json.RawMessage{}, msgs2raw(msgs[:brief+1])...)
	out = append(out, marker)
	out = append(out, msgs2raw(msgs[tail:])...)
	slim, err := json.Marshal(out)
	if err != nil {
		return raw, false
	}
	return slim, true
}

func msgs2raw(msgs []map[string]json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(msgs))
	for _, m := range msgs {
		raw, _ := json.Marshal(m)
		out = append(out, raw)
	}
	return out
}
