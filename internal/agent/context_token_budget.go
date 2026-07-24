package agent

import (
	"context"
	"fmt"
	"sync"
)

// tokenBudget is a minimal CreditTracker that caps a single worker's
// total token spend. The loop calls Record after every model turn with
// that turn's input+output tokens; once the running total exceeds the
// cap, Record returns an error which the loop turns into an ErrorEvent,
// stopping the worker. The partial report is preserved by runWorkerLoop.
//
// It is deliberately independent of the credits package: a worker's cap
// is a local safety limit (runaway loop protection), not the user's
// billing budget, so it must not consume or interact with that ledger.
type tokenBudget struct {
	limit int64

	mu   sync.Mutex
	used int64
}

// newTokenBudget returns a tracker that stops the loop once total tokens
// exceed limit. A limit of 0 or less means "no cap"; callers should not
// build one in that case, but Record then never trips.
func newTokenBudget(limit int64) *tokenBudget {
	return &tokenBudget{limit: limit}
}

// Record adds this turn's tokens to the running total and returns an
// error when the cap is exceeded. Matches the CreditTracker contract:
// any non-nil return short-circuits the run.
func (b *tokenBudget) Record(_ context.Context, in, out int64, _ string) error {
	b.mu.Lock()
	b.used += in + out
	total := b.used
	b.mu.Unlock()
	if b.limit > 0 && total > b.limit {
		return fmt.Errorf("worker token budget exceeded (%d > %d)", total, b.limit)
	}
	return nil
}

// SessionCap reports the worker's token limit so budget-based
// eviction can key off the cap rather than tokens already spent.
func (b *tokenBudget) SessionCap() int64 {
	return b.limit
}

// Used reports the running total for both the session and daily slots
// (a worker has no daily concept, so they are the same).
func (b *tokenBudget) Used() (session, daily int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used, b.used
}
