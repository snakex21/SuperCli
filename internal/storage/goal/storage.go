package goal

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Storage persists goals and tasks to the SuperCli
// SQLite database. Safe for concurrent use (database/sql
// is). The Storage does NOT own the *sql.DB — the caller
// creates the DB via storage.Open and shares it with
// memory, session, credits, etc.
type Storage struct {
	db *sql.DB
}

// NewStorage wraps db for goal access. db must have
// already run Migrate (which is idempotent).
func NewStorage(db *sql.DB) *Storage {
	return &Storage{db: db}
}

// Migrate creates the goals and goal_tasks tables +
// indexes if they don't exist. Idempotent. Call this at
// startup, right after storage.Open.
func (s *Storage) Migrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("goal: Storage.Migrate: nil db")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS goals (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			success_criteria TEXT NOT NULL DEFAULT '',
			notes TEXT NOT NULL DEFAULT '',
			verification_status TEXT NOT NULL DEFAULT '',
			verification_evidence TEXT NOT NULL DEFAULT '',
			verified_at INTEGER,
			status TEXT NOT NULL DEFAULT 'active',
			created_at INTEGER NOT NULL,
			completed_at INTEGER,
			parent_session_id TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS goals_status_idx
			ON goals(status, created_at)`,
		`CREATE TABLE IF NOT EXISTS goal_tasks (
			id TEXT PRIMARY KEY,
			goal_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			title TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at INTEGER NOT NULL,
			completed_at INTEGER,
			FOREIGN KEY (goal_id) REFERENCES goals(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS goal_tasks_goal_idx
			ON goal_tasks(goal_id, seq)`,
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("goal: Migrate exec: %w", err)
		}
	}
	// Existing portable databases predate verification. SQLite has no portable
	// ADD COLUMN IF NOT EXISTS form, so inspect the schema before each additive
	// migration. This keeps Migrate idempotent without rebuilding the table.
	for _, col := range []struct {
		name string
		ddl  string
	}{
		{"verification_status", "TEXT NOT NULL DEFAULT ''"},
		{"verification_evidence", "TEXT NOT NULL DEFAULT ''"},
		{"verified_at", "INTEGER"},
	} {
		if err := s.ensureGoalColumn(ctx, col.name, col.ddl); err != nil {
			return err
		}
	}
	return nil
}

func (s *Storage) ensureGoalColumn(ctx context.Context, name, ddl string) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(goals)`)
	if err != nil {
		return fmt.Errorf("goal: inspect goals schema: %w", err)
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var column, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &column, &typ, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("goal: scan goals schema: %w", err)
		}
		if column == name {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE goals ADD COLUMN `+name+` `+ddl); err != nil {
		return fmt.Errorf("goal: add goals.%s: %w", name, err)
	}
	return nil
}

// CreateGoal inserts a new goal and returns it (with
// generated id, timestamps populated).
func (s *Storage) CreateGoal(ctx context.Context, g *Goal) error {
	if s == nil || s.db == nil {
		return ErrNotFound // misuse, but matches the read path
	}
	if g.Title == "" {
		return ErrEmptyTitle
	}
	if g.Status == "" {
		g.Status = StatusActive
	}
	if err := validateStatus(g.Status); err != nil {
		return err
	}
	if !ValidVerificationStatus(g.VerificationStatus) {
		return fmt.Errorf("goal: invalid verification status %q", g.VerificationStatus)
	}
	if g.ID == "" {
		g.ID = generateID(defaultRandBytes)
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO goals
			(id, title, description, success_criteria, notes, verification_status, verification_evidence,
			 verified_at, status, created_at, completed_at, parent_session_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.ID, g.Title, g.Description, g.SuccessCriteria, g.Notes, string(g.VerificationStatus), g.VerificationEvidence,
		nullableTime(g.VerifiedAt), string(g.Status),
		g.CreatedAt.UnixNano(), nullableTime(g.CompletedAt), nullableString(g.ParentSessionID),
	)
	if err != nil {
		return fmt.Errorf("goal: CreateGoal: %w", err)
	}
	return nil
}

// GetGoal fetches a goal by id. Returns ErrNotFound if
// no row matches.
func (s *Storage) GetGoal(ctx context.Context, id string) (*Goal, error) {
	if s == nil || s.db == nil {
		return nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT id, title, description, success_criteria, notes,
		        verification_status, verification_evidence, verified_at,
		        status, created_at, completed_at, parent_session_id
		 FROM goals WHERE id = ?`, id)
	return scanGoal(row)
}

// ActiveGoal returns the most-recently-created goal with
// status=active. Returns nil and no error if no active
// goal exists. The "most recent" order is by created_at
// DESC; ties are broken by id (descending) for stability.
func (s *Storage) ActiveGoal(ctx context.Context) (*Goal, error) {
	if s == nil || s.db == nil {
		return nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT id, title, description, success_criteria, notes,
		        verification_status, verification_evidence, verified_at,
		        status, created_at, completed_at, parent_session_id
		 FROM goals WHERE status = 'active'
		 ORDER BY created_at DESC, id DESC LIMIT 1`)
	g, err := scanGoal(row)
	if err == ErrNotFound {
		return nil, nil
	}
	return g, err
}

// ListGoals returns all goals, newest first.
func (s *Storage) ListGoals(ctx context.Context) ([]*Goal, error) {
	if s == nil || s.db == nil {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, description, success_criteria, notes,
		        verification_status, verification_evidence, verified_at,
		        status, created_at, completed_at, parent_session_id
		 FROM goals ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("goal: ListGoals: %w", err)
	}
	defer rows.Close()
	var out []*Goal
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UpdateGoalStatus sets the status and (if terminal)
// the completed_at timestamp. The caller is responsible
// for knowing whether the transition is allowed.
func (s *Storage) UpdateGoalStatus(ctx context.Context, id string, status Status) error {
	if err := validateStatus(status); err != nil {
		return err
	}
	var completedAt any
	if status == StatusDone || status == StatusAbandoned {
		completedAt = time.Now().UnixNano()
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE goals SET status = ?, completed_at = ? WHERE id = ?`,
		string(status), completedAt, id,
	)
	if err != nil {
		return fmt.Errorf("goal: UpdateGoalStatus: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// AppendNote appends a timestamped line to the goal's
// notes. Existing notes are preserved. The first line of
// the appended text is the user-supplied content; we
// prepend a `[\<ISO time\>]` stamp for auditability.
func (s *Storage) AppendNote(ctx context.Context, id, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("goal: AppendNote: empty text")
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	var current string
	row := s.db.QueryRowContext(ctx,
		`SELECT notes FROM goals WHERE id = ?`, id)
	if err := row.Scan(&current); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return err
	}
	joined := current
	if joined != "" {
		joined += "\n"
	}
	joined += "[" + stamp + "] " + text
	_, err := s.db.ExecContext(ctx,
		`UPDATE goals SET notes = ? WHERE id = ?`, joined, id,
	)
	if err != nil {
		return fmt.Errorf("goal: AppendNote: %w", err)
	}
	return nil
}

// AddTask appends a new task to a goal. The seq field
// is auto-assigned (max(seq)+1 within the goal).
func (s *Storage) AddTask(ctx context.Context, goalID, title string) (*Task, error) {
	if title == "" {
		return nil, ErrEmptyTitle
	}
	t := &Task{
		ID:        generateTaskID(defaultRandBytes),
		GoalID:    goalID,
		Title:     title,
		Status:    TaskPending,
		CreatedAt: time.Now(),
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("goal: AddTask begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var goalStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM goals WHERE id = ?`, goalID).Scan(&goalStatus); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("goal: AddTask goal status: %w", err)
	}
	if Status(goalStatus) != StatusActive {
		return nil, fmt.Errorf("goal: cannot add a task to a %s goal", goalStatus)
	}
	var nextSeq int
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM goal_tasks WHERE goal_id = ?`,
		goalID,
	).Scan(&nextSeq)
	if err != nil {
		return nil, fmt.Errorf("goal: AddTask seq: %w", err)
	}
	t.Seq = nextSeq
	_, err = tx.ExecContext(ctx,
		`INSERT INTO goal_tasks (id, goal_id, seq, title, status, created_at, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, NULL)`,
		t.ID, t.GoalID, t.Seq, t.Title, string(t.Status), t.CreatedAt.UnixNano(),
	)
	if err != nil {
		return nil, fmt.Errorf("goal: AddTask insert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, clearVerificationSQL, goalID); err != nil {
		return nil, fmt.Errorf("goal: AddTask invalidate verification: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("goal: AddTask commit: %w", err)
	}
	return t, nil
}

// ListTasks returns the tasks for a goal, ordered by seq.
func (s *Storage) ListTasks(ctx context.Context, goalID string) ([]Task, error) {
	if s == nil || s.db == nil {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, goal_id, seq, title, status, created_at, completed_at
		 FROM goal_tasks WHERE goal_id = ? ORDER BY seq ASC`, goalID,
	)
	if err != nil {
		return nil, fmt.Errorf("goal: ListTasks: %w", err)
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// SetTaskStatus updates a task's status. seq is 1-based
// (matches ListTasks display). Returns ErrNotFound if
// the (goalID, seq) pair has no row.
func (s *Storage) SetTaskStatus(ctx context.Context, goalID string, seq int, status Status) error {
	if !ValidTaskStatus(status) {
		return fmt.Errorf("%w: %q", ErrInvalidStatus, status)
	}
	var completedAt any
	if status == TaskDone || status == TaskSkipped {
		completedAt = time.Now().UnixNano()
	} else {
		completedAt = nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("goal: SetTaskStatus begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE goal_tasks SET status = ?, completed_at = ?
		 WHERE goal_id = ? AND seq = ?
		   AND EXISTS (SELECT 1 FROM goals WHERE id = ? AND status = 'active')`,
		string(status), completedAt, goalID, seq, goalID,
	)
	if err != nil {
		return fmt.Errorf("goal: SetTaskStatus: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, clearVerificationSQL, goalID); err != nil {
		return fmt.Errorf("goal: SetTaskStatus invalidate verification: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("goal: SetTaskStatus commit: %w", err)
	}
	return nil
}

const clearVerificationSQL = `UPDATE goals
	SET verification_status = '', verification_evidence = '', verified_at = NULL
	WHERE id = ?`

// SetVerification stores the latest explicit outcome and its evidence.
func (s *Storage) SetVerification(ctx context.Context, goalID string, status VerificationStatus, evidence string) error {
	if status != VerificationPassed && status != VerificationFailed {
		return fmt.Errorf("goal: invalid verification status %q", status)
	}
	evidence = strings.TrimSpace(evidence)
	if evidence == "" {
		return fmt.Errorf("goal: verification evidence is empty")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE goals SET verification_status = ?, verification_evidence = ?, verified_at = ?
		 WHERE id = ? AND status = 'active'
		   AND NOT EXISTS (
		     SELECT 1 FROM goal_tasks
		     WHERE goal_id = ? AND status NOT IN ('done', 'skipped')
		   )`,
		string(status), evidence, time.Now().UnixNano(), goalID, goalID,
	)
	if err != nil {
		return fmt.Errorf("goal: SetVerification: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM goals WHERE id = ?`, goalID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrNotFound
		}
		_, _, open, err := s.TaskProgress(ctx, goalID)
		if err != nil {
			return err
		}
		if open > 0 {
			return fmt.Errorf("%w: %d", ErrOpenTasks, open)
		}
		g, err := s.GetGoal(ctx, goalID)
		if err != nil {
			return err
		}
		return fmt.Errorf("goal: cannot verify goal with status %s", g.Status)
	}
	return nil
}

// CompleteVerifiedGoal atomically enforces the final contract. The conditional
// update cannot race with a task mutation because both are SQLite writes.
func (s *Storage) CompleteVerifiedGoal(ctx context.Context, goalID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE goals SET status = 'done', completed_at = ?
		 WHERE id = ? AND status = 'active'
		   AND verification_status = 'passed' AND TRIM(verification_evidence) <> ''
		   AND NOT EXISTS (
		     SELECT 1 FROM goal_tasks
		     WHERE goal_id = ? AND status NOT IN ('done', 'skipped')
		   )`,
		time.Now().UnixNano(), goalID, goalID,
	)
	if err != nil {
		return fmt.Errorf("goal: CompleteVerifiedGoal: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	g, err := s.GetGoal(ctx, goalID)
	if err != nil {
		return err
	}
	_, _, open, err := s.TaskProgress(ctx, goalID)
	if err != nil {
		return err
	}
	if open > 0 {
		return fmt.Errorf("%w: %d", ErrOpenTasks, open)
	}
	if g.VerificationStatus != VerificationPassed || strings.TrimSpace(g.VerificationEvidence) == "" {
		return ErrVerificationRequired
	}
	return fmt.Errorf("goal: cannot complete goal with status %s", g.Status)
}

// TaskProgress returns total, terminal and open task counts. Done and skipped
// tasks are terminal; pending and in-progress tasks remain open.
func (s *Storage) TaskProgress(ctx context.Context, goalID string) (total, terminal, open int, err error) {
	if s == nil || s.db == nil {
		return 0, 0, 0, ErrNotFound
	}
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        COALESCE(SUM(CASE WHEN status IN ('done', 'skipped') THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN status NOT IN ('done', 'skipped') THEN 1 ELSE 0 END), 0)
		 FROM goal_tasks WHERE goal_id = ?`, goalID).Scan(&total, &terminal, &open)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("goal: TaskProgress: %w", err)
	}
	return total, terminal, open, nil
}

// CountTasks returns the number of tasks and the number
// of done tasks for a goal. Cheap (two COUNT queries).
func (s *Storage) CountTasks(ctx context.Context, goalID string) (total, done int, err error) {
	if s == nil || s.db == nil {
		return 0, 0, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END), 0)
		 FROM goal_tasks WHERE goal_id = ?`, goalID)
	if err := row.Scan(&total, &done); err != nil {
		return 0, 0, fmt.Errorf("goal: CountTasks: %w", err)
	}
	return total, done, nil
}

// scannable is the shared shape for *sql.Row and
// *sql.Rows. Both have Scan(dest ...any) error.
type scannable interface {
	Scan(dest ...any) error
}

func scanGoal(r scannable) (*Goal, error) {
	var g Goal
	var status string
	var verificationStatus string
	var createdAt int64
	var completedAt sql.NullInt64
	var verifiedAt sql.NullInt64
	var parentSessionID sql.NullString
	if err := r.Scan(
		&g.ID, &g.Title, &g.Description, &g.SuccessCriteria, &g.Notes,
		&verificationStatus, &g.VerificationEvidence, &verifiedAt,
		&status, &createdAt, &completedAt, &parentSessionID,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("goal: scanGoal: %w", err)
	}
	g.Status = Status(status)
	g.VerificationStatus = VerificationStatus(verificationStatus)
	g.CreatedAt = time.Unix(0, createdAt)
	if verifiedAt.Valid {
		t := time.Unix(0, verifiedAt.Int64)
		g.VerifiedAt = &t
	}
	if completedAt.Valid {
		t := time.Unix(0, completedAt.Int64)
		g.CompletedAt = &t
	}
	if parentSessionID.Valid {
		g.ParentSessionID = parentSessionID.String
	}
	return &g, nil
}

func scanTask(r scannable) (*Task, error) {
	var t Task
	var status string
	var createdAt int64
	var completedAt sql.NullInt64
	if err := r.Scan(
		&t.ID, &t.GoalID, &t.Seq, &t.Title, &status, &createdAt, &completedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("goal: scanTask: %w", err)
	}
	t.Status = Status(status)
	t.CreatedAt = time.Unix(0, createdAt)
	if completedAt.Valid {
		ct := time.Unix(0, completedAt.Int64)
		t.CompletedAt = &ct
	}
	return &t, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixNano()
}
