package goal

import (
	"context"
	"fmt"
)

const goalColumns = `id, title, description, success_criteria, notes, verification_status, verification_evidence, verified_at, status, created_at, completed_at, parent_session_id, project_key`

// ActiveForProject prefers the project's own goal; only an explicitly global
// goal can be a fallback. Unassigned legacy goals never enter another project.
func (s *Storage) ActiveForProject(ctx context.Context, key string) (*Goal, error) {
	if key == "" {
		return s.ActiveGoal(ctx)
	}
	row := s.db.QueryRowContext(ctx, "SELECT "+goalColumns+` FROM goals WHERE status='active' AND project_key IN (?, ?) ORDER BY CASE WHEN project_key=? THEN 0 ELSE 1 END, created_at DESC, id DESC LIMIT 1`, key, GlobalProjectKey, key)
	g, err := scanGoal(row)
	if err == ErrNotFound {
		return nil, nil
	}
	return g, err
}

func (s *Storage) listScope(ctx context.Context, key string, legacy bool) ([]*Goal, error) {
	clause := "project_key IN (?, ?)"
	args := []any{key, GlobalProjectKey}
	if legacy {
		clause, args = "project_key = ''", nil
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+goalColumns+" FROM goals WHERE "+clause+" ORDER BY created_at DESC, id DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Goal{}
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// replaceActive is atomic: creating a goal pauses only the previous goal in
// the same scope. A failed insert rolls back the pause.
func (s *Storage) replaceActive(ctx context.Context, g *Goal) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE goals SET status='paused', completed_at=NULL WHERE status='active' AND project_key=?`, g.ProjectKey); err != nil {
		return err
	}
	if err := s.createGoal(ctx, g, tx.ExecContext); err != nil {
		return err
	}
	return tx.Commit()
}

// moveGoal keeps tasks, notes and verification attached to the same id.
func (s *Storage) moveGoal(ctx context.Context, id, key string, activate bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status Status
	if err := tx.QueryRowContext(ctx, `SELECT status FROM goals WHERE id=?`, id).Scan(&status); err != nil {
		return err
	}
	if activate {
		status = StatusActive
	}
	if status == StatusActive {
		if _, err := tx.ExecContext(ctx, `UPDATE goals SET status='paused', completed_at=NULL WHERE status='active' AND project_key=? AND id<>?`, key, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE goals SET project_key=?, status=?, completed_at=CASE WHEN ?='active' THEN NULL ELSE completed_at END WHERE id=?`, key, status, status, id); err != nil {
		return err
	}
	return tx.Commit()
}

func NewProjectService(storage *Storage, projectKey string) *Service {
	return &Service{storage: storage, projectKey: projectKey}
}

func (s *Service) visible(g *Goal) bool {
	return g != nil && (s.projectKey == "" || g.ProjectKey == s.projectKey || g.ProjectKey == GlobalProjectKey)
}

func (s *Service) Unassigned(ctx context.Context) ([]*Goal, error) {
	return s.storage.listScope(ctx, "", true)
}

func (s *Service) ListAll(ctx context.Context) ([]*Goal, error) {
	return s.storage.ListGoals(ctx)
}

func (s *Service) SetGlobal(ctx context.Context, title, description, criteria, parent string) (*Goal, error) {
	global := NewProjectService(s.storage, GlobalProjectKey)
	g, err := global.Set(ctx, title, description, criteria, parent)
	if err == nil {
		_, err = s.Refresh(ctx)
	}
	return g, err
}

// Assign accepts only legacy goals or goals already visible in this project.
// An unknown scope cannot be used to reach a different project's goal.
func (s *Service) Assign(ctx context.Context, id string, global bool) error {
	g, err := s.storage.GetGoal(ctx, id)
	if err != nil {
		return err
	}
	if g.ProjectKey != "" && !s.visible(g) {
		return ErrNotFound
	}
	key := s.projectKey
	if global {
		key = GlobalProjectKey
	}
	if key == "" {
		return fmt.Errorf("goal: project scope is not configured")
	}
	if err := s.storage.moveGoal(ctx, id, key, false); err != nil {
		return err
	}
	_, err = s.Refresh(ctx)
	return err
}

// GlobalService is an independent view over the same shared database.
func (s *Service) GlobalService() *Service {
	return NewProjectService(s.storage, GlobalProjectKey)
}
