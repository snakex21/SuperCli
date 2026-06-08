// Package credits tracks token usage per session and per
// day, enforces budget caps, and persists the ledger to
// the SuperCli SQLite database. The package is used by
// the agent loop to short-circuit over-budget runs and by
// the TUI / --status flag to display current usage.
//
// Three layers:
//
//   1. Budget  (budget.go)        — caps (PerSession, PerDay)
//   2. Tracker (tracker.go)       — accumulates usage, enforces caps
//   3. Storage (storage.go)       — SQLite persistence (ledger + budget rows)
//   4. Cost    (cost.go)          — per-model USD rate → credit conversion
//   5. Audit   (audit.go)         — non-blocking append-only log of tool calls
//
// F7 keeps credits in TOKENS (not USD). The cost layer
// gives a rough USD estimate but the authoritative number
// the user sees is the token total.
package credits

import "errors"

// ErrBudgetExceeded is returned by Tracker.Record when
// recording a new delta would push the run past its
// PerSession or PerDay cap. The caller should treat this
// as a hard stop and surface it to the user.
var ErrBudgetExceeded = errors.New("credits: budget exceeded")

// Source identifies where usage came from. The ledger
// uses this to slice the data later (e.g. "how much did
// the explore sub-agent spend?").
type Source string

const (
	SourceLoop      Source = "loop"
	SourceSubAgent  Source = "subagent"
	SourceDarwin    Source = "darwin"
	SourceJudge     Source = "judge"
	SourceReflector Source = "reflector"
)

// Valid reports whether s is one of the known sources.
// Empty string is treated as valid (= loop default).
func (s Source) Valid() bool {
	switch s {
	case "", SourceLoop, SourceSubAgent, SourceDarwin, SourceJudge, SourceReflector:
		return true
	}
	return false
}
