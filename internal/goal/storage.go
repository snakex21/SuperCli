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
	if g.ID == "" {
		g.ID = generateID(defaultRandBytes)
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO goals
			(id, title, description, success_criteria, notes, status, created_at, completed_at, parent_session_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.ID, g.Title, g.Description, g.SuccessCriteria, g.Notes, string(g.Status),
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
	var nextSeq int
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM goal_tasks WHERE goal_id = ?`,
		goalID,
	).Scan(&nextSeq)
	if err != nil {
		return nil, fmt.Errorf("goal: AddTask seq: %w", err)
	}
	t.Seq = nextSeq
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO goal_tasks (id, goal_id, seq, title, status, created_at, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?, NULL)`,
		t.ID, t.GoalID, t.Seq, t.Title, string(t.Status), t.CreatedAt.UnixNano(),
	)
	if err != nil {
		return nil, fmt.Errorf("goal: AddTask insert: %w", err)
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
	res, err := s.db.ExecContext(ctx,
		`UPDATE goal_tasks SET status = ?, completed_at = ?
		 WHERE goal_id = ? AND seq = ?`,
		string(status), completedAt, goalID, seq,
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
	return nil
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
	var createdAt int64
	var completedAt sql.NullInt64
	var parentSessionID sql.NullString
	if err := r.Scan(
		&g.ID, &g.Title, &g.Description, &g.SuccessCriteria, &g.Notes,
		&status, &createdAt, &completedAt, &parentSessionID,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("goal: scanGoal: %w", err)
	}
	g.Status = Status(status)
	g.CreatedAt = time.Unix(0, createdAt)
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
