package app

import (
	"fmt"
	"strings"

	"supercli/internal/account/credits"
	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/goal"
	"supercli/internal/ui/tui"
)

// The dashboard reads counters and cached goal state; it does not scan history
// or query the goal database on each spinner tick.
type statusBarDeps struct {
	goalSvc          *goal.Service
	loop             *agent.Loop
	tracker          *credits.Tracker
	home             string
	hasActiveProject bool
	projectName      string
}

func buildDashboardFn(d statusBarDeps) func() tui.DashboardSnapshot {
	return func() tui.DashboardSnapshot {
		s := tui.DashboardSnapshot{Directory: shortenDir(d.home), Orchestrator: supercliOrchestratorMode}
		if d.hasActiveProject {
			s.Project = d.projectName
		}
		s.Goal = d.goalSvc.Progress()
		if d.tracker != nil {
			s.SessionTokens, s.DailyTokens = d.tracker.Used()
			budget := d.tracker.Budget()
			s.SessionCap, s.DailyCap = budget.PerSession, budget.PerDay
		}
		if d.loop == nil {
			return s
		}
		provider := llm.Unwrap(d.loop.Provider())
		if rp, ok := provider.(interface {
			RateLimits() (llm.CodexRateLimits, bool)
		}); ok {
			if rl, ok := rp.RateLimits(); ok {
				s.Limits = rl.FormatHUD()
			}
		}
		if rt, ok := provider.(*llm.RouterProvider); ok {
			snaps, _, active := rt.PoolUsage()
			if len(snaps) > 1 {
				s.Account = fmt.Sprintf("%s (%d/%d)", rt.ActiveLabel(), active+1, len(snaps))
				summary := rt.PoolUsageSummary()
				s.Limits = strings.TrimSpace(s.Limits + fmt.Sprintf(" · %d/%d", summary.Available, summary.Accounts))
			}
		}
		return s
	}
}
