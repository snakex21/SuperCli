package goal

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Service is the in-memory front of the goal package.
// It holds a pointer to the active goal, exposes a
// thread-safe Refresh from SQLite, and renders the
// `[current_goal]` block the agent loop prepends to the
// system prompt.
//
// Service is safe for concurrent use.
type Service struct {
	storage *Storage

	mu        sync.RWMutex
	active    *Goal
	activeID  string // last-known active id; used to detect drift
	loadedAt  time.Time
	loadedErr error
}

// NewService builds a Service. The active goal is NOT
// loaded eagerly; call Refresh before Inject. main.go
// calls Refresh once at startup and after every
// `/goal` slash command.
func NewService(storage *Storage) *Service {
	return &Service{storage: storage}
}

// Refresh reloads the active goal from SQLite. Safe to
// call concurrently. Returns the active goal (or nil)
// and any error.
func (s *Service) Refresh(ctx context.Context) (*Goal, error) {
	if s == nil || s.storage == nil {
		return nil, fmt.Errorf("goal: Service.Refresh: nil storage")
	}
	g, err := s.storage.ActiveGoal(ctx)
	s.mu.Lock()
	s.active = g
	s.loadedAt = time.Now()
	s.loadedErr = err
	if g != nil {
		s.activeID = g.ID
	} else {
		s.activeID = ""
	}
	s.mu.Unlock()
	return g, err
}

// Active returns the current in-memory active goal. nil
// if none. Does NOT touch SQLite; for the freshest view
// call Refresh first.
func (s *Service) Active() *Goal {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

// Set creates a new active goal. The current active
// (if any) is automatically moved to `paused` so the
// per-home "one active" invariant holds. Returns the
// new goal.
func (s *Service) Set(ctx context.Context, title, description, criteria, parentSession string) (*Goal, error) {
	if s == nil || s.storage == nil {
		return nil, fmt.Errorf("goal: Service.Set: nil storage")
	}
	if strings.TrimSpace(title) == "" {
		return nil, ErrEmptyTitle
	}
	// Pause any current active.
	cur, _ := s.storage.ActiveGoal(ctx)
	if cur != nil {
		if err := s.storage.UpdateGoalStatus(ctx, cur.ID, StatusPaused); err != nil {
			return nil, fmt.Errorf("goal: pause prior active: %w", err)
		}
	}
	g := &Goal{
		Title:           strings.TrimSpace(title),
		Description:     description,
		SuccessCriteria: criteria,
		Status:          StatusActive,
		ParentSessionID: parentSession,
	}
	if err := s.storage.CreateGoal(ctx, g); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.active = g
	s.activeID = g.ID
	s.loadedAt = time.Now()
	s.mu.Unlock()
	return g, nil
}

// AddTask appends a task to a goal. If goalID is empty,
// the task is added to the active goal. Refreshes the
// in-memory state.
func (s *Service) AddTask(ctx context.Context, goalID, title string) (*Task, error) {
	if s == nil || s.storage == nil {
		return nil, fmt.Errorf("goal: Service.AddTask: nil storage")
	}
	if goalID == "" {
		g := s.Active()
		if g == nil {
			return nil, fmt.Errorf("goal: AddTask: no active goal; specify --goal")
		}
		goalID = g.ID
	}
	task, err := s.storage.AddTask(ctx, goalID, title)
	if err == nil {
		s.refreshIfActive(ctx, goalID)
	}
	return task, err
}

// SetTaskStatus updates a task's status.
func (s *Service) SetTaskStatus(ctx context.Context, goalID string, seq int, status Status) error {
	if s == nil || s.storage == nil {
		return fmt.Errorf("goal: Service.SetTaskStatus: nil storage")
	}
	if goalID == "" {
		g := s.Active()
		if g == nil {
			return fmt.Errorf("goal: SetTaskStatus: no active goal")
		}
		goalID = g.ID
	}
	if err := s.storage.SetTaskStatus(ctx, goalID, seq, status); err != nil {
		return err
	}
	s.refreshIfActive(ctx, goalID)
	return nil
}

// Verify records a foreground check of the complete task set against the
// goal's success criteria (or title when criteria are omitted). Evidence is
// mandatory so a pass is auditable rather than a bare boolean.
func (s *Service) Verify(ctx context.Context, goalID string, passed bool, evidence string) error {
	if s == nil || s.storage == nil {
		return fmt.Errorf("goal: Service.Verify: nil storage")
	}
	goalID, err := s.resolveGoalID(goalID)
	if err != nil {
		return err
	}
	_, _, open, err := s.storage.TaskProgress(ctx, goalID)
	if err != nil {
		return err
	}
	if open > 0 {
		return fmt.Errorf("%w: %d", ErrOpenTasks, open)
	}
	status := VerificationFailed
	if passed {
		status = VerificationPassed
	}
	if err := s.storage.SetVerification(ctx, goalID, status, evidence); err != nil {
		return err
	}
	s.refreshIfActive(ctx, goalID)
	return nil
}

// SetStatus updates a goal's status. Empty goalID means
// the active goal. After a status change away from
// active, the in-memory active pointer is cleared.
func (s *Service) SetStatus(ctx context.Context, goalID string, status Status) error {
	if s == nil || s.storage == nil {
		return fmt.Errorf("goal: Service.SetStatus: nil storage")
	}
	if goalID == "" {
		g := s.Active()
		if g == nil {
			return fmt.Errorf("goal: SetStatus: no active goal")
		}
		goalID = g.ID
	}
	if status == StatusDone {
		if err := s.storage.CompleteVerifiedGoal(ctx, goalID); err != nil {
			return err
		}
	} else {
		if err := s.storage.UpdateGoalStatus(ctx, goalID, status); err != nil {
			return err
		}
	}
	if status != StatusActive {
		s.mu.Lock()
		if s.active != nil && s.active.ID == goalID {
			s.active = nil
			s.activeID = ""
		}
		s.mu.Unlock()
	}
	return nil
}

func (s *Service) resolveGoalID(goalID string) (string, error) {
	if goalID != "" {
		return goalID, nil
	}
	g := s.Active()
	if g == nil {
		return "", fmt.Errorf("goal: no active goal")
	}
	return g.ID, nil
}

func (s *Service) refreshIfActive(ctx context.Context, goalID string) {
	s.mu.RLock()
	isActive := s.active != nil && s.active.ID == goalID
	s.mu.RUnlock()
	if isActive {
		_, _ = s.Refresh(ctx)
	}
}

// AppendNote appends a timestamped note to a goal.
// Empty goalID means the active goal.
func (s *Service) AppendNote(ctx context.Context, goalID, text string) error {
	if s == nil || s.storage == nil {
		return fmt.Errorf("goal: Service.AppendNote: nil storage")
	}
	if goalID == "" {
		g := s.Active()
		if g == nil {
			return fmt.Errorf("goal: AppendNote: no active goal")
		}
		goalID = g.ID
	}
	return s.storage.AppendNote(ctx, goalID, text)
}

// ListTasks returns the tasks for a goal (or active).
func (s *Service) ListTasks(ctx context.Context, goalID string) ([]Task, error) {
	if s == nil || s.storage == nil {
		return nil, fmt.Errorf("goal: Service.ListTasks: nil storage")
	}
	if goalID == "" {
		g := s.Active()
		if g == nil {
			return nil, nil
		}
		goalID = g.ID
	}
	return s.storage.ListTasks(ctx, goalID)
}

// Goal returns a single goal by id.
func (s *Service) Goal(ctx context.Context, id string) (*Goal, error) {
	if s == nil || s.storage == nil {
		return nil, fmt.Errorf("goal: Service.Goal: nil storage")
	}
	return s.storage.GetGoal(ctx, id)
}

// List returns all goals.
func (s *Service) List(ctx context.Context) ([]*Goal, error) {
	if s == nil || s.storage == nil {
		return nil, fmt.Errorf("goal: Service.List: nil storage")
	}
	return s.storage.ListGoals(ctx)
}

// Inject returns systemBase with a `[current_goal]`
// block appended, or systemBase unchanged if there is
// no active goal. The block lists the goal's title,
// description, success criteria, and the first
// maxTasks pending/in_progress tasks.
//
// This is the function the agent loop calls once at
// session start. It does NOT mutate state.
func (s *Service) Inject(ctx context.Context, systemBase string, maxTasks int) (string, error) {
	if s == nil {
		return systemBase, nil
	}
	if maxTasks <= 0 {
		maxTasks = 5
	}
	g := s.Active()
	if g == nil {
		return systemBase, nil
	}
	tasks, err := s.storage.ListTasks(ctx, g.ID)
	if err != nil {
		return systemBase, err
	}
	pending := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		if t.Status == TaskDone || t.Status == TaskSkipped {
			continue
		}
		pending = append(pending, t)
		if len(pending) >= maxTasks {
			break
		}
	}
	var b strings.Builder
	b.WriteString(systemBase)
	b.WriteString("\n\n[current_goal]\n")
	fmt.Fprintf(&b, "title: %s\n", g.Title)
	fmt.Fprintf(&b, "goal_id: %s\n", g.ID)
	if g.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", g.Description)
	}
	if g.SuccessCriteria != "" {
		fmt.Fprintf(&b, "success_criteria: %s\n", g.SuccessCriteria)
	}
	if len(pending) > 0 {
		b.WriteString("open_tasks:\n")
		for _, t := range pending {
			mark := "pending"
			if t.Status == TaskInProgress {
				mark = "in_progress"
			}
			fmt.Fprintf(&b, "  - [%s] %d. %s\n", mark, t.Seq, t.Title)
		}
		b.WriteString("state_updates: use the goal tool; start_task, complete_task, verify, and mark_done are action values, not tool names. Omit goal_id to use this active goal.\n")
	} else if g.VerificationStatus == VerificationPassed {
		b.WriteString("verification: passed\n")
		fmt.Fprintf(&b, "verification_evidence: %s\n", compactVerificationEvidence(g.VerificationEvidence, 240))
		b.WriteString("completion_ready: true (call goal mark_done only after the final response is ready)\n")
	} else {
		b.WriteString("verification_required: true\n")
		if g.VerificationStatus == VerificationFailed {
			fmt.Fprintf(&b, "last_verification: failed — %s\n", compactVerificationEvidence(g.VerificationEvidence, 240))
		}
		b.WriteString("instruction: verify the result against success_criteria (or the goal title), then call goal verify with passed and concrete evidence; mark_done is blocked until verification passes.\n")
	}
	b.WriteString("[end current_goal]\n")
	return b.String(), nil
}

func compactVerificationEvidence(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// StatusLine returns a short, single-line summary of
// the active goal for the TUI footer. Returns "" when
// no active goal — the TUI omits the line entirely.
//
// Format: "goal: <title> (done/total tasks)".
func (s *Service) StatusLine(ctx context.Context) string {
	if s == nil {
		return ""
	}
	g := s.Active()
	if g == nil {
		return ""
	}
	total, terminal, open, err := s.storage.TaskProgress(ctx, g.ID)
	if err != nil || total == 0 {
		return fmt.Sprintf("goal: %s", g.Title)
	}
	if open == 0 {
		switch g.VerificationStatus {
		case VerificationPassed:
			return fmt.Sprintf("goal: %s (%d/%d tasks, verified)", g.Title, terminal, total)
		case VerificationFailed:
			return fmt.Sprintf("goal: %s (%d/%d tasks, verification failed)", g.Title, terminal, total)
		default:
			return fmt.Sprintf("goal: %s (%d/%d tasks, verify)", g.Title, terminal, total)
		}
	}
	return fmt.Sprintf("goal: %s (%d/%d tasks)", g.Title, terminal, total)
}
