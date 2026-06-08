package credits

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// Storage persists the credit ledger and budget rows to
// the SuperCli SQLite database. Safe for concurrent use
// (database/sql is, the mutex just guards the schema
// migration idempotency check).
//
// The Storage does NOT own the *sql.DB — the caller
// creates the DB via storage.Open and shares it with
// memory / session / stats. The credits package only
// owns the credit_* tables.
type Storage struct {
	db   *sql.DB
	once sync.Once
}

// NewStorage wraps db for credit ledger access. db must
// have the credit_ledger and credit_budgets tables (the
// caller is expected to have run Migrate, which is
// idempotent).
func NewStorage(db *sql.DB) *Storage {
	return &Storage{db: db}
}

// Migrate creates the credit_* tables and indexes if
// they don't exist. Idempotent — safe to call on every
// startup.
func (s *Storage) Migrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("credits: Storage.Migrate: nil db")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS credit_ledger (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			ts INTEGER NOT NULL,
			turn_seq INTEGER NOT NULL,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			source TEXT NOT NULL DEFAULT 'loop',
			parent_session_id TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS credit_ledger_session_idx
			ON credit_ledger(session_id, ts)`,
		`CREATE INDEX IF NOT EXISTS credit_ledger_day_idx
			ON credit_ledger(ts)`,
		`CREATE TABLE IF NOT EXISTS credit_budgets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL UNIQUE,
			per_session_cap INTEGER NOT NULL,
			per_day_cap INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("credits: Migrate exec: %w", err)
		}
	}
	return nil
}

// AppendLedger inserts a single ledger row. Source may
// be empty (= loop). Returns the inserted row id.
func (s *Storage) AppendLedger(ctx context.Context, e LedgerEntry) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("credits: AppendLedger: nil storage")
	}
	if e.SessionID == "" {
		return 0, fmt.Errorf("credits: AppendLedger: empty session_id")
	}
	if e.TS == 0 {
		e.TS = time.Now().UnixNano()
	}
	if !e.Source.Valid() {
		return 0, fmt.Errorf("credits: AppendLedger: invalid source %q", e.Source)
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO credit_ledger
			(session_id, ts, turn_seq, input_tokens, output_tokens, source, parent_session_id)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.SessionID, e.TS, e.TurnSeq, e.Input, e.Output, string(e.Source), nullableString(e.ParentSessionID),
	)
	if err != nil {
		return 0, fmt.Errorf("credits: AppendLedger insert: %w", err)
	}
	return res.LastInsertId()
}

// LedgerEntry is a single row in the credit_ledger table.
// Field names match the SQL columns exactly.
type LedgerEntry struct {
	SessionID       string
	TS              int64 // unix nano; 0 = now
	TurnSeq         int
	Input           int64
	Output          int64
	Source          Source
	ParentSessionID string // empty = no parent (top-level loop)
}

// SessionTotal returns the total tokens (input+output)
// recorded against sessionID.
func (s *Storage) SessionTotal(ctx context.Context, sessionID string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("credits: SessionTotal: nil storage")
	}
	if sessionID == "" {
		return 0, fmt.Errorf("credits: SessionTotal: empty session_id")
	}
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(input_tokens + output_tokens), 0)
		 FROM credit_ledger WHERE session_id = ?`, sessionID,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("credits: SessionTotal query: %w", err)
	}
	return total.Int64, nil
}

// DailyTotal returns the total tokens recorded since
// midnight UTC. It sums across all sessions.
func (s *Storage) DailyTotal(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("credits: DailyTotal: nil storage")
	}
	midnight := time.Now().UTC().Truncate(24 * time.Hour).UnixNano()
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(input_tokens + output_tokens), 0)
		 FROM credit_ledger WHERE ts >= ?`, midnight,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("credits: DailyTotal query: %w", err)
	}
	return total.Int64, nil
}

// SaveBudget inserts (or updates on conflict) the budget
// row for a session. The unique index on session_id
// means a second call replaces the caps.
func (s *Storage) SaveBudget(ctx context.Context, sessionID string, b Budget) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("credits: SaveBudget: nil storage")
	}
	if err := b.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO credit_budgets
			(session_id, per_session_cap, per_day_cap, created_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(session_id) DO UPDATE SET
				per_session_cap = excluded.per_session_cap,
				per_day_cap = excluded.per_day_cap,
				created_at = excluded.created_at`,
		sessionID, b.PerSession, b.PerDay, time.Now().UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("credits: SaveBudget: %w", err)
	}
	return nil
}

// LoadBudget returns the budget row for a session. If
// no row exists, returns the zero Budget and a nil
// error — callers can decide what to do.
func (s *Storage) LoadBudget(ctx context.Context, sessionID string) (Budget, error) {
	if s == nil || s.db == nil {
		return Budget{}, fmt.Errorf("credits: LoadBudget: nil storage")
	}
	var b Budget
	err := s.db.QueryRowContext(ctx,
		`SELECT per_session_cap, per_day_cap
		 FROM credit_budgets WHERE session_id = ?`, sessionID,
	).Scan(&b.PerSession, &b.PerDay)
	if err == sql.ErrNoRows {
		return Budget{}, nil
	}
	if err != nil {
		return Budget{}, fmt.Errorf("credits: LoadBudget: %w", err)
	}
	return b, nil
}

// nullableString returns nil if s is empty, otherwise &s.
// Used for inserting into nullable TEXT columns.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
