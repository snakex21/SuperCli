package goal

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

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
