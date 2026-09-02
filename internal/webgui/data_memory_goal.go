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
	svc, err := e.goalService(ctx)
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
	return &goalView{
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
	svc, err := e.goalService(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := svc.Refresh(ctx); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(in.Action) {
	case "set":
		_, err = svc.Set(ctx, in.Title, strings.TrimSpace(in.Description), strings.TrimSpace(in.SuccessCriteria), strings.TrimSpace(in.ParentSessionID))
	case "add_task":
		_, err = svc.AddTask(ctx, "", in.Title)
	case "set_task_status":
		status := goal.Status(strings.TrimSpace(in.Status))
		if !goal.ValidTaskStatus(status) {
			return nil, fmt.Errorf("invalid task status %q", in.Status)
		}
		if in.TaskSeq <= 0 {
			return nil, fmt.Errorf("task_seq must be positive")
		}
		err = svc.SetTaskStatus(ctx, "", in.TaskSeq, status)
	case "add_note":
		err = svc.AppendNote(ctx, "", in.Text)
	case "verify":
		if in.Passed == nil {
			return nil, fmt.Errorf("verify requires passed")
		}
		err = svc.Verify(ctx, "", *in.Passed, in.Text)
	case "set_status":
		status := goal.Status(strings.TrimSpace(in.Status))
		if status != goal.StatusDone && status != goal.StatusAbandoned {
			return nil, fmt.Errorf("invalid terminal goal status %q", in.Status)
		}
		err = svc.SetStatus(ctx, "", status)
	default:
		return nil, fmt.Errorf("unknown goal action %q", in.Action)
	}
	if err != nil {
		return nil, err
	}
	return e.activeGoal(ctx)
}
