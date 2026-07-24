package app

import (
	"context"

	"supercli/internal/account/credits"
	"supercli/internal/storage/goal"
)

// ultraworkGoalAdapter bridges *goal.Service to the
// ultrawork.GoalGate interface. Defined here (not in the
// ultrawork package) so the ultrawork package does not
// have to import goal.
type ultraworkGoalAdapter struct {
	svc *goal.Service
}

func (g ultraworkGoalAdapter) ActiveID() string {
	if g.svc == nil {
		return ""
	}
	a := g.svc.Active()
	if a == nil {
		return ""
	}
	return a.ID
}

func (g ultraworkGoalAdapter) ActiveTitle() string {
	if g.svc == nil {
		return ""
	}
	a := g.svc.Active()
	if a == nil {
		return ""
	}
	return a.Title
}

func (g ultraworkGoalAdapter) UnfinishedTasks(ctx context.Context) int {
	if g.svc == nil {
		return 0
	}
	a := g.svc.Active()
	if a == nil {
		return 0
	}
	tasks, err := g.svc.ListTasks(ctx, a.ID)
	if err != nil {
		return 0
	}
	n := 0
	for _, t := range tasks {
		if t.Status == goal.TaskDone || t.Status == goal.TaskSkipped {
			continue
		}
		n++
	}
	return n
}

// ultraworkCreditAdapter bridges *credits.Tracker to the
// ultrawork.CreditGate interface. Defined here (not in
// the ultrawork package) so the ultrawork package does
// not have to import credits.
type ultraworkCreditAdapter struct {
	tracker *credits.Tracker
}

func (c ultraworkCreditAdapter) Remaining(ctx context.Context) (int64, int64) {
	if c.tracker == nil {
		return 0, 0
	}
	// credits.Tracker has no Remaining() method, only
	// Used(). Compute the gap from the budget.
	budget := c.tracker.Budget()
	sessUsed, dayUsed := c.tracker.Used()
	sess := budget.PerSession - sessUsed
	if sess < 0 {
		sess = 0
	}
	day := budget.PerDay - dayUsed
	if day < 0 {
		day = 0
	}
	return sess, day
}

func (c ultraworkCreditAdapter) HasBudget() bool {
	if c.tracker == nil {
		return false
	}
	b := c.tracker.Budget()
	return b.PerSession > 0 || b.PerDay > 0
}
