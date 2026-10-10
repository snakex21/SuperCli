package checkpoint

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrTurnSealed       = errors.New("checkpoint turn no longer accepts mutations")
	ErrCompletionBusy   = errors.New("checkpoint completion is already running")
	ErrWrongTurn        = errors.New("checkpoint worker binding belongs to another turn")
	ErrCommitIncomplete = errors.New("checkpoint completion did not establish a commit point")
)

type turnPhase uint8

const (
	turnActive turnPhase = iota
	turnSealing
	turnRecoverable
	turnRecorded
	turnClosed
)

var completedTurnSignal = func() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}()

// TurnBarrier protects accepted mutating tool calls and worker invocations.
// Its zero value is active. It performs no filesystem I/O and holds no lock
// while the actual tool, worker or completion callbacks execute.
type TurnBarrier struct {
	mu        sync.Mutex
	phase     turnPhase
	members   int
	drained   chan struct{}
	finishing bool
}

// TurnBorrow belongs to one worker invocation, never its retained registry.
type TurnBorrow struct {
	barrier *TurnBarrier
	closed  bool // Protected by barrier.mu.
}

func (b *TurnBarrier) addLocked() {
	if b.drained == nil {
		b.drained = make(chan struct{})
	}
	b.members++
}

func (b *TurnBarrier) leaveLocked() {
	b.members--
	if b.members == 0 && b.phase != turnActive {
		close(b.drained)
	}
}

// Borrow accepts a worker before its goroutine/context is detached.
func (b *TurnBarrier) Borrow(ctx context.Context) (*TurnBorrow, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b.phase != turnActive {
		return nil, ErrTurnSealed
	}
	b.addLocked()
	return &TurnBorrow{barrier: b}, nil
}

func (borrow *TurnBorrow) Close() {
	if borrow == nil {
		return
	}
	b := borrow.barrier
	b.mu.Lock()
	defer b.mu.Unlock()
	if !borrow.closed {
		borrow.closed = true
		b.leaveLocked()
	}
}

func (b *TurnBarrier) checkBorrowLocked(borrow *TurnBorrow) error {
	if borrow.barrier != b {
		return ErrWrongTurn
	}
	if borrow.closed || b.phase == turnRecorded || b.phase == turnClosed {
		return ErrTurnSealed
	}
	return nil
}

func (b *TurnBarrier) checkBorrow(borrow *TurnBorrow) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.checkBorrowLocked(borrow)
}

// EnterMutation covers before capture AND the actual Fn until it returns.
// New unbound calls are refused after sealing; a previously accepted borrower
// can finish its invocation while Complete waits, including further tool calls.
func (b *TurnBarrier) EnterMutation(ctx context.Context, borrow *TurnBorrow) (func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if borrow != nil {
		if err := b.checkBorrowLocked(borrow); err != nil {
			return nil, err
		}
	} else if b.phase != turnActive {
		return nil, ErrTurnSealed
	}
	b.addLocked()
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.leaveLocked()
			b.mu.Unlock()
		})
	}, nil
}

// CompletionReady is an event for an already requested completion. It is nil
// while the turn is active. After sealing, no new unbound member can invalidate
// the signal; an accepted borrow remains a member until its entire run ends.
// A caller may use it for ONE completion retry after a timeout, not a retry loop.
func (b *TurnBarrier) CompletionReady() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.phase == turnActive {
		return nil
	}
	if b.members == 0 {
		return completedTurnSignal
	}
	return b.drained
}

// Seal lets a UI defer only turns that still own accepted work. If immediate
// is true, Complete can run inline; otherwise exactly one caller-owned goroutine
// can wait on ready and then create a fresh completion timeout. No goroutine or
// timer is allocated here. A canceled UI context must not cancel that waiter.
func (b *TurnBarrier) Seal() (ready <-chan struct{}, immediate bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.phase == turnActive {
		b.phase = turnSealing
	}
	if b.members == 0 {
		return completedTurnSignal, true
	}
	return b.drained, false
}

// Complete seals even when ctx is already canceled. Waiting is event-driven;
// the caller must not hold Turn.mu or Manager.mu while invoking this method.
// commit captures/pins after and establishes a committed append (or a verified
// no-op). It returns committed=true at that point even if a later step fails.
// release unpins active snapshots only after this point. Failure preserves the
// appropriate recovery/recorded state; a recorded retry only invokes release.
// The parent Turn retains the committed Record for idempotent result return.
func (b *TurnBarrier) Complete(ctx context.Context, commit func(context.Context) (bool, error), release func(context.Context) error) (err error) {
	b.mu.Lock()
	if b.phase == turnClosed {
		b.mu.Unlock()
		return nil
	}
	if b.finishing {
		b.mu.Unlock()
		return ErrCompletionBusy
	}
	b.finishing = true
	recorded := b.phase == turnRecorded
	if !recorded {
		b.phase = turnSealing
	}
	drained := b.drained
	if b.members == 0 {
		drained = nil
	}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.finishing = false
		if b.phase == turnSealing {
			b.phase = turnRecoverable
		}
		b.mu.Unlock()
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if drained != nil {
		select {
		case <-drained:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !recorded {
		committed, commitErr := commit(ctx)
		if committed {
			b.mu.Lock()
			b.phase = turnRecorded
			b.mu.Unlock()
		}
		if commitErr != nil {
			return commitErr
		}
		if !committed {
			return ErrCommitIncomplete
		}
	}
	if err := release(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	b.phase = turnClosed
	b.mu.Unlock()
	return nil
}

// recordedCompletionReady admits only cleanup retry; it cannot reopen after capture.
func (b *TurnBarrier) recordedCompletionReady() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.phase == turnRecorded && b.members == 0 && !b.finishing
}
