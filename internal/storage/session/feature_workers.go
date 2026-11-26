package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"supercli/internal/llm"
)

// WorkerRow is a delegated worker's resumable state: its own transcript (the
// worker's conversation, never the parent chat) plus identity and status. A
// worker survives an interrupted run or an app restart through this row.
type WorkerRow struct {
	SessionID   string
	ID          string
	Agent       string
	Description string
	Model       string
	BaseDir     string
	Status      string
	LastError   string
	Runs        int
	// CommandsRun / ChecksRun: lifetime counts of commands and checks the
	// worker ran (kept across resumes and restarts).
	CommandsRun int
	ChecksRun   int
	Messages    []llm.Message
	UpdatedAt   time.Time
}

// SaveWorker upserts one worker row.
func (s *Store) SaveWorker(ctx context.Context, row WorkerRow) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("session.Store.SaveWorker: nil store")
	}
	if row.SessionID == "" || row.ID == "" {
		return fmt.Errorf("session.Store.SaveWorker: session and worker id are required")
	}
	raw, err := json.Marshal(row.Messages)
	if err != nil {
		return fmt.Errorf("worker transcript encode: %w", err)
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = time.Now()
	}
	_, err = s.db.ExecContext(ctx, `
  INSERT INTO session_workers(session_id,worker_id,agent,description,model,base_dir,status,last_error,runs,messages_json,updated_at,commands_run,checks_run)
  VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
  ON CONFLICT(session_id,worker_id) DO UPDATE SET agent=excluded.agent, description=excluded.description,
    model=excluded.model, base_dir=excluded.base_dir, status=excluded.status, last_error=excluded.last_error,
    runs=excluded.runs, messages_json=excluded.messages_json, updated_at=excluded.updated_at,
    commands_run=MAX(session_workers.commands_run, excluded.commands_run), checks_run=MAX(session_workers.checks_run, excluded.checks_run)`,
		row.SessionID, row.ID, row.Agent, row.Description, row.Model, row.BaseDir, row.Status, row.LastError, row.Runs, raw, row.UpdatedAt.UnixMilli(),
		row.CommandsRun, row.ChecksRun)
	return err
}

// SetWorkerStatus changes only the status/reason of a saved worker.
func (s *Store) SetWorkerStatus(ctx context.Context, sessionID, id, status, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE session_workers SET status=?, last_error=?, updated_at=? WHERE session_id=? AND worker_id=?`,
		status, reason, time.Now().UnixMilli(), sessionID, id)
	return err
}

func scanWorkerRow(scan func(...any) error) (WorkerRow, error) {
	var row WorkerRow
	var raw []byte
	var updated int64
	if err := scan(&row.SessionID, &row.ID, &row.Agent, &row.Description, &row.Model, &row.BaseDir, &row.Status, &row.LastError, &row.Runs, &raw, &updated, &row.CommandsRun, &row.ChecksRun); err != nil {
		return row, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &row.Messages); err != nil {
			return row, fmt.Errorf("worker transcript decode: %w", err)
		}
	}
	row.UpdatedAt = time.UnixMilli(updated)
	return row, nil
}

const workerColumns = `session_id,worker_id,agent,description,model,base_dir,status,last_error,runs,messages_json,updated_at,commands_run,checks_run`

// LoadWorker reads one saved worker; ok=false when none exists.
func (s *Store) LoadWorker(ctx context.Context, sessionID, id string) (WorkerRow, bool, error) {
	row, err := scanWorkerRow(s.db.QueryRowContext(ctx, `SELECT `+workerColumns+` FROM session_workers WHERE session_id=? AND worker_id=?`, sessionID, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkerRow{}, false, nil
	}
	if err != nil {
		return WorkerRow{}, false, err
	}
	return row, true, nil
}

// ListWorkers returns a session's saved workers (transcripts included).
func (s *Store) ListWorkers(ctx context.Context, sessionID string) ([]WorkerRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+workerColumns+` FROM session_workers WHERE session_id=? ORDER BY updated_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkerRow
	for rows.Next() {
		row, err := scanWorkerRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ListWorkerSummaries returns a session's saved workers without transcripts
// (Messages nil), oldest first; activeOnly keeps "created"/"running" rows.
// Listings and status polls use it: selecting messages_json for every worker
// read hundreds of MB per GUI poll in a session with ~400 workers.
func (s *Store) ListWorkerSummaries(ctx context.Context, sessionID string, activeOnly bool) ([]WorkerRow, error) {
	query := `SELECT session_id,worker_id,agent,description,model,base_dir,status,last_error,runs,updated_at,commands_run,checks_run FROM session_workers WHERE session_id=?`
	if activeOnly {
		query += ` AND status IN ('created','running')`
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY updated_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkerRow
	for rows.Next() {
		var row WorkerRow
		var updated int64
		if err := rows.Scan(&row.SessionID, &row.ID, &row.Agent, &row.Description, &row.Model, &row.BaseDir, &row.Status, &row.LastError, &row.Runs, &updated, &row.CommandsRun, &row.ChecksRun); err != nil {
			return nil, err
		}
		row.UpdatedAt = time.UnixMilli(updated)
		out = append(out, row)
	}
	return out, rows.Err()
}

// WorkerBrief is a saved worker without its transcript.
type WorkerBrief struct {
	ID, Agent, Description, Status string
	CommandsRun, ChecksRun         int
}

// ListWorkerBriefs returns a session's saved workers without transcripts
// (cheap: used to refuse repeating a worker's task in an automatic turn).
func (s *Store) ListWorkerBriefs(ctx context.Context, sessionID string) ([]WorkerBrief, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT worker_id, agent, description, status, commands_run, checks_run FROM session_workers WHERE session_id=?`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkerBrief
	for rows.Next() {
		var b WorkerBrief
		if err := rows.Scan(&b.ID, &b.Agent, &b.Description, &b.Status, &b.CommandsRun, &b.ChecksRun); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// WorkerIDs lists every saved worker id (all sessions), so a new process never
// reuses an id that a saved worker still answers to.
func (s *Store) WorkerIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT worker_id FROM session_workers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
