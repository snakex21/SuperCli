package webgui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"supercli/internal/storage/goal"
	"supercli/internal/storage/memory"
)

func (e *Engine) memoryList(scope string, limit int) ([]memoryItem, error) {
	out := []memoryItem{}
	if gs, err := memory.OpenStore(e.dataDir); err == nil {
		defer gs.Close()
		if entries, err := gs.List(scope, limit); err == nil {
			out = append(out, toMemoryItems(entries, "global")...)
		}
	}
	if ps, err := memory.OpenProjectStore(e.dataDir, e.Home()); err == nil {
		defer ps.Close()
		if entries, err := ps.List(scope, limit); err == nil {
			out = append(out, toMemoryItems(entries, "project")...)
		}
	}
	return out, nil
}

// toMemoryItems converts store entries to the wire form.
func toMemoryItems(entries []memory.Entry, target string) []memoryItem {
	out := make([]memoryItem, 0, len(entries))
	for _, en := range entries {
		out = append(out, memoryItem{
			ID:        en.ID,
			Scope:     en.Scope,
			Target:    target,
			Content:   en.Content,
			Tags:      en.Tags,
			Source:    en.Source,
			UpdatedAt: en.UpdatedAt.Format(time.RFC3339),
		})
	}
	return out
}

// activeGoal returns the current goal and its tasks, or nil when no goal is
// set. Refresh observes changes made by the TUI or another running instance.
func (e *Engine) activeGoal(ctx context.Context) (*goalView, error) {
	return e.activeGoalScope(ctx, "project")
}

func (e *Engine) activeGoalScope(ctx context.Context, scope string) (*goalView, error) {
	svc, err := e.goalServiceScope(ctx, e.Home(), scope)
	if err != nil {
		return nil, err
	}
	if _, err := svc.Refresh(ctx); err != nil {
		return nil, err
	}
	g := svc.Active()
	if g == nil {
		return nil, nil
	}
	tasks, err := svc.ListTasks(ctx, g.ID)
	if err != nil {
		return nil, err
	}
	tv := make([]taskView, 0, len(tasks))
	open := 0
	for _, t := range tasks {
		tv = append(tv, taskView{Seq: t.Seq, Title: t.Title, Status: string(t.Status)})
		if t.Status != goal.TaskDone && t.Status != goal.TaskSkipped {
			open++
		}
	}
	verifiedAt := ""
	if g.VerifiedAt != nil {
		verifiedAt = g.VerifiedAt.Format(time.RFC3339)
	}
	scopeName := "project"
	if g.ProjectKey == goal.GlobalProjectKey {
		scopeName = "global"
	}
	return &goalView{
		Scope:                scopeName,
		ID:                   g.ID,
		Title:                g.Title,
		Description:          g.Description,
		SuccessCriteria:      g.SuccessCriteria,
		Notes:                g.Notes,
		Status:               string(g.Status),
		VerificationStatus:   string(g.VerificationStatus),
		VerificationEvidence: g.VerificationEvidence,
		VerifiedAt:           verifiedAt,
		ReadyForVerification: open == 0,
		CanFinish:            open == 0 && g.VerificationStatus == goal.VerificationPassed,
		Tasks:                tv,
	}, nil
}

// mutateGoal applies one bounded UI operation and returns the fresh active
// view. Goal history remains in SQLite when a goal is completed or abandoned.
func (e *Engine) mutateGoal(ctx context.Context, in goalMutation) (*goalView, error) {
	home := e.Home()
	svc, err := e.goalServiceScope(ctx, home, in.Scope)
	if err != nil {
		return nil, err
	}
	if _, err := svc.Refresh(ctx); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(in.Action) {
	case "set":
		_, err = svc.Set(ctx, in.Title, strings.TrimSpace(in.Description), strings.TrimSpace(in.SuccessCriteria), strings.TrimSpace(in.ParentSessionID))
	case "assign":
		if in.Target != "project" && in.Target != "global" {
			return nil, fmt.Errorf("invalid target scope")
		}
		projectSvc, serviceErr := e.goalServiceAt(ctx, home)
		if serviceErr != nil {
			return nil, serviceErr
		}
		err = projectSvc.Assign(ctx, in.GoalID, in.Target == "global")
	case "resume":
		err = svc.SetStatus(ctx, in.GoalID, goal.StatusActive)
	case "add_task":
		_, err = svc.AddTask(ctx, in.GoalID, in.Title)
	case "set_task_status":
		status := goal.Status(strings.TrimSpace(in.Status))
		if !goal.ValidTaskStatus(status) {
			return nil, fmt.Errorf("invalid task status %q", in.Status)
		}
		if in.TaskSeq <= 0 {
			return nil, fmt.Errorf("task_seq must be positive")
		}
		err = svc.SetTaskStatus(ctx, in.GoalID, in.TaskSeq, status)
	case "add_note":
		err = svc.AppendNote(ctx, in.GoalID, in.Text)
	case "verify":
		if in.Passed == nil {
			return nil, fmt.Errorf("verify requires passed")
		}
		err = svc.Verify(ctx, in.GoalID, *in.Passed, in.Text)
	case "set_status":
		status := goal.Status(strings.TrimSpace(in.Status))
		if status != goal.StatusDone && status != goal.StatusAbandoned {
			return nil, fmt.Errorf("invalid terminal goal status %q", in.Status)
		}
		err = svc.SetStatus(ctx, in.GoalID, status)
	default:
		return nil, fmt.Errorf("unknown goal action %q", in.Action)
	}
	if err != nil {
		return nil, err
	}
	return e.activeGoalScope(ctx, in.Scope)
}

type goalSummary struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Scope  string `json:"scope"`
}

func summarizeGoals(goals []*goal.Goal, pausedOnly bool) []goalSummary {
	out := []goalSummary{}
	for _, g := range goals {
		if pausedOnly && g.Status != goal.StatusPaused {
			continue
		}
		scope := "project"
		if g.ProjectKey == goal.GlobalProjectKey {
			scope = "global"
		} else if g.ProjectKey == "" {
			scope = "unassigned"
		}
		out = append(out, goalSummary{ID: g.ID, Title: g.Title, Status: string(g.Status), Scope: scope})
	}
	return out
}

func (e *Engine) goalCatalog(ctx context.Context, scope string) (map[string][]goalSummary, error) {
	svc, err := e.goalServiceScope(ctx, e.Home(), scope)
	if err != nil {
		return nil, err
	}
	old, err := svc.Unassigned(ctx)
	if err != nil {
		return nil, err
	}
	goals, err := svc.List(ctx)
	if err != nil {
		return nil, err
	}
	return map[string][]goalSummary{"unassigned": summarizeGoals(old, false), "paused": summarizeGoals(goals, true)}, nil
}
