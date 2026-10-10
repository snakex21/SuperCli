package checkpoint

import (
	"context"
	"errors"
)

// The manager owns admitted turns until checkpoint cleanup succeeds. This
// retains only the bounded checkpoint state, never a worker Loop, provider,
// registry or request context. A popped controller/SSE run therefore cannot
// lose the owner after a transient persistence/release failure.
func (m *Manager) retainTurn(t *Turn) {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	if m.pending == nil {
		m.pending = make(map[*Turn]struct{})
	}
	m.pending[t] = struct{}{}
}
func (m *Manager) releaseTurn(t *Turn) {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()
	delete(m.pending, t)
	if len(m.pending) == 0 {
		m.pending = nil
	}
}

// detachPendingUserSequences handles a late checkpoint from a removed chat
// tail. Snapshot pending ownership without holding it while taking Turn.mu:
// lease admission already uses the opposite Turn.mu -> pendingMu order.
// Caller must invoke this BEFORE acquiring Manager.mu or StoreIO, because a
// concurrent Complete holds Turn.mu while appending under the store gate.
// This protects this Manager's in-process owners. An independent Manager or
// process needs a durable immutable message receipt to detach its own turn.
func (m *Manager) detachPendingUserSequences(sessionID string, fromSeq int) {
	m.pendingMu.Lock()
	turns := make([]*Turn, 0, len(m.pending))
	for turn := range m.pending {
		turns = append(turns, turn)
	}
	m.pendingMu.Unlock()
	for _, turn := range turns {
		turn.mu.Lock()
		if turn.sessionID == sessionID && turn.userSeq >= fromSeq {
			turn.detachedUserSeq = true
			turn.userSeq = 0
		}
		turn.mu.Unlock()
	}
}

// RetryRecordedCompletions retries cleanup only for a turn whose after+record
// already committed. It never recaptures the workspace after an interrupted
// run, which could otherwise assign later manual edits to the old turn. This
// is a single attempt on an explicit operation, with no timer or retry loop.
func (m *Manager) RetryRecordedCompletions(ctx context.Context) error {
	m.pendingMu.Lock()
	turns := make([]*Turn, 0, len(m.pending))
	for turn := range m.pending {
		turns = append(turns, turn)
	}
	m.pendingMu.Unlock()
	var failure error
	for _, turn := range turns {
		if !turn.barrier.recordedCompletionReady() {
			continue
		}
		if _, err := turn.Complete(ctx); err != nil && !errors.Is(err, ErrCompletionBusy) {
			failure = errors.Join(failure, err)
		}
	}
	return failure
}
