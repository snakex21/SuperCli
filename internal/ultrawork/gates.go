package ultrawork

import (
	"context"
	"fmt"
)

// GoalGate is the ultrawork-side view of the active /goal. The
// concrete adapter (built in main.go) wraps *goal.Service and
// surfaces only the three things ultrawork needs:
//
//   - Is there an active goal at all?
//   - What is its title (for status / TUI)?
//   - How many tasks are NOT done or skipped?
//
// Keeping the interface narrow means the ultrawork package
// does not import goal, and the unit tests can supply a tiny
// stub.
type GoalGate interface {
	// ActiveID returns the active goal's id, or "" if no
	// goal is active. Cheap; safe to call on every turn.
	ActiveID() string
	// ActiveTitle returns the active goal's title, or ""
	// when there is no active goal. Used for the
	// CheckGates error message and the Sisyphus reminder.
	ActiveTitle() string
	// UnfinishedTasks returns the count of tasks on the
	// active goal whose status is not done or skipped.
	// Returns 0 when there is no active goal. Errors are
	// treated as 0 (best-effort; the worst case is one
	// extra loop turn).
	UnfinishedTasks(ctx context.Context) int
}

// CreditGate is the ultrawork-side view of the F7 credit
// budget. The concrete adapter wraps *credits.Tracker and
// exposes Remaining(session, daily int64).
//
// A nil CreditGate is treated as "no budget check" — useful
// for tests and for the F7 disabled case.
type CreditGate interface {
	// Remaining returns the tokens left in the current
	// session and the current UTC day. A return of (0, 0)
	// means "nothing left" only when the corresponding
	// budget cap is set; with no caps at all, HasBudget
	// returns false and Remaining is ignored.
	Remaining(ctx context.Context) (session, daily int64)
	// HasBudget reports whether at least one cap is
	// configured. When false, CheckGates does not block
	// the run on credits.
	HasBudget() bool
}

// GateResult is the verdict of CheckGates.
type GateResult struct {
	// OK is true when both gates pass.
	OK bool
	// Reason is a human-readable explanation. Set when
	// OK is false (the reason for the failure) AND when
	// OK is true (a one-line "all gates clear" so the
	// TUI can echo it).
	Reason string
}

// CheckGates evaluates the two F9 gates in order. A missing
// GoalGate is the most common failure (user typed "ulw" but
// never ran /goal set); we surface that first because it is
// almost always the user's next action. A missing or empty
// CreditGate is allowed when HasBudget is false; it is
// rejected when HasBudget is true and Remaining is zero on
// at least one cap.
//
// Both arguments may be nil; nil goal gate fails the check,
// nil credit gate passes when HasBudget would be false.
func CheckGates(goal GoalGate, credit CreditGate) GateResult {
	if goal == nil {
		return GateResult{
			OK:     false,
			Reason: "no /goal active — set one with `/goal set <title>` first; ultrawork needs a target",
		}
	}
	id := goal.ActiveID()
	if id == "" {
		title := goal.ActiveTitle()
		if title != "" {
			return GateResult{
				OK:     false,
				Reason: "no /goal active (title=" + title + " but no active id) — refresh with `/goal show` or set a new one",
			}
		}
		return GateResult{
			OK:     false,
			Reason: "no /goal active — set one with `/goal set <title>` first; ultrawork needs a target",
		}
	}
	if credit == nil {
		// No credit tracker wired = the F7 feature is
		// off. We treat this as "no cap", which is the
		// same as HasBudget=false.
		return GateResult{OK: true, Reason: "all gates clear (credit gate not wired; F7 off)"}
	}
	if !credit.HasBudget() {
		return GateResult{OK: true, Reason: "all gates clear (no credit budget configured)"}
	}
	// HasBudget = true. The gate is satisfied as long as
	// the session OR the day has at least one token left
	// (we want to let the user in if EITHER ceiling has
	// room; the F7 tracker will hard-stop mid-run on the
	// first one that hits zero).
	sess, daily := credit.Remaining(context.Background())
	if sess == 0 && daily == 0 {
		return GateResult{
			OK:     false,
			Reason: fmt.Sprintf("out of credits (session remaining: 0, daily remaining: 0) — wait for the daily reset or raise --max-credits-session"),
		}
	}
	return GateResult{
		OK:     true,
		Reason: fmt.Sprintf("all gates clear (session remaining: %d tokens, daily remaining: %d tokens)", sess, daily),
	}
}
