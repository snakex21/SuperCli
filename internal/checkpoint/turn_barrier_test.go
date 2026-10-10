package checkpoint

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// Done signals entry into Complete's blocking select without sleeps or polls.
type turnBarrierWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *turnBarrierWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestTurnBarrierCompleteProtectsActualFn(t *testing.T) {
	var barrier TurnBarrier
	leave, err := barrier.EnterMutation(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer leave()
	ctx := &turnBarrierWaitContext{Context: context.Background(), waiting: make(chan struct{})}
	var commits, releases atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- barrier.Complete(ctx, func(context.Context) (bool, error) {
			commits.Add(1)
			return true, nil
		}, func(context.Context) error {
			releases.Add(1)
			return nil
		})
	}()
	<-ctx.waiting
	if commits.Load() != 0 || releases.Load() != 0 {
		t.Fatal("after capture entered before actual Fn returned")
	}
	if finish, err := barrier.EnterMutation(context.Background(), nil); !errors.Is(err, ErrTurnSealed) {
		if finish != nil {
			finish()
		}
		t.Fatalf("late mutation accepted: %v", err)
	}
	if err := barrier.Complete(context.Background(), nil, nil); !errors.Is(err, ErrCompletionBusy) {
		t.Fatal(err)
	}
	leave()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if commits.Load() != 1 || releases.Load() != 1 {
		t.Fatal("completion did not commit and release once")
	}
}

func TestTurnBarrierSealAndBorrowerFnDrain(t *testing.T) {
	var barrier TurnBarrier
	if barrier.CompletionReady() != nil {
		t.Fatal("active turn already exposed a completion event")
	}
	borrow, err := barrier.Borrow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer borrow.Close()
	ready, immediate := barrier.Seal()
	if immediate || ready == nil || ready != barrier.CompletionReady() {
		t.Fatal("seal did not expose the pending invocation event")
	}
	if _, err := barrier.Borrow(context.Background()); !errors.Is(err, ErrTurnSealed) {
		t.Fatal(err)
	}
	finish, err := barrier.EnterMutation(context.Background(), borrow)
	if err != nil {
		t.Fatalf("accepted borrower could not finish after seal: %v", err)
	}
	defer finish()
	borrow.Close()
	borrow.Close()
	select {
	case <-ready:
		t.Fatal("borrow release ignored actual Fn")
	default:
	}
	finish()
	<-ready
	if _, err := barrier.EnterMutation(context.Background(), borrow); !errors.Is(err, ErrTurnSealed) {
		t.Fatal(err)
	}
	if _, immediate := barrier.Seal(); !immediate {
		t.Fatal("drained turn was not immediately completable")
	}
}

func TestTurnBarrierCanceledCompleteSealsAndRetainsRecovery(t *testing.T) {
	var barrier TurnBarrier
	borrow, err := barrier.Borrow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer borrow.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	commits, releases := 0, 0
	commit := func(context.Context) (bool, error) { commits++; return true, nil }
	release := func(context.Context) error { releases++; return nil }
	if err := barrier.Complete(ctx, commit, release); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if commits != 0 || releases != 0 || barrier.CompletionReady() == nil {
		t.Fatal("cancellation discarded recovery or invoked completion I/O")
	}
	if _, err := barrier.EnterMutation(context.Background(), nil); !errors.Is(err, ErrTurnSealed) {
		t.Fatalf("already canceled completion left turn open: %v", err)
	}
	finish, err := barrier.EnterMutation(context.Background(), borrow)
	if err != nil {
		t.Fatalf("cancellation discarded accepted worker: %v", err)
	}
	finish()
	ready := barrier.CompletionReady()
	borrow.Close()
	<-ready
	if err := barrier.Complete(context.Background(), commit, release); err != nil {
		t.Fatal(err)
	}
	if commits != 1 || releases != 1 {
		t.Fatal("recovery retry did not finish exactly once")
	}
}

func TestTurnBarrierCancellationWhileWaiting(t *testing.T) {
	var barrier TurnBarrier
	borrow, err := barrier.Borrow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer borrow.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waitCtx := &turnBarrierWaitContext{Context: ctx, waiting: make(chan struct{})}
	var callbacks atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- barrier.Complete(waitCtx, func(context.Context) (bool, error) {
			callbacks.Add(1)
			return true, nil
		}, func(context.Context) error {
			callbacks.Add(1)
			return nil
		})
	}()
	<-waitCtx.waiting
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if callbacks.Load() != 0 {
		t.Fatal("wait cancellation released protected roots")
	}
}

func TestTurnBarrierCompletionRetriesCommitAndCleanupSeparately(t *testing.T) {
	var barrier TurnBarrier
	commits, releases := 0, 0
	fault := errors.New("synthetic checkpoint write failure")
	commit := func(context.Context) (bool, error) {
		commits++
		if commits == 1 {
			return false, fault
		}
		return true, nil
	}
	release := func(context.Context) error {
		releases++
		if releases == 1 {
			return fault
		}
		return nil
	}
	if err := barrier.Complete(context.Background(), commit, release); !errors.Is(err, fault) || releases != 0 {
		t.Fatalf("failed append unpinned roots: %v", err)
	}
	if err := barrier.Complete(context.Background(), commit, release); !errors.Is(err, fault) {
		t.Fatal(err)
	}
	if err := barrier.Complete(context.Background(), commit, release); err != nil {
		t.Fatal(err)
	}
	if err := barrier.Complete(context.Background(), commit, release); err != nil {
		t.Fatal(err)
	}
	if commits != 2 || releases != 2 {
		t.Fatal("recorded or closed retry appended again")
	}
}

func TestTurnBarrierCommitPointSurvivesLaterError(t *testing.T) {
	var barrier TurnBarrier
	commits, releases := 0, 0
	fault := errors.New("synthetic post-commit gate close failure")
	commit := func(context.Context) (bool, error) { commits++; return true, fault }
	release := func(context.Context) error { releases++; return nil }
	if err := barrier.Complete(context.Background(), commit, release); !errors.Is(err, fault) || releases != 0 {
		t.Fatal("post-commit error unexpectedly unpinned roots")
	}
	if err := barrier.Complete(context.Background(), commit, release); err != nil {
		t.Fatal(err)
	}
	if commits != 1 || releases != 1 {
		t.Fatal("post-commit retry repeated append")
	}
}

func TestTurnBarrierUncommittedSuccessCannotRelease(t *testing.T) {
	var barrier TurnBarrier
	released := false
	err := barrier.Complete(context.Background(), func(context.Context) (bool, error) {
		return false, nil
	}, func(context.Context) error {
		released = true
		return nil
	})
	if !errors.Is(err, ErrCommitIncomplete) || released {
		t.Fatalf("uncommitted completion released roots: %v", err)
	}
}

func TestTurnBarrierReusesOneEventAndRejectsWrongBorrow(t *testing.T) {
	var barrier, other TurnBarrier
	first, err := barrier.EnterMutation(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	firstEvent := barrier.drained
	first()
	second, err := barrier.EnterMutation(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if barrier.drained != firstEvent {
		t.Fatal("ordinary Fn allocated another drain channel")
	}
	second()
	borrow, err := other.Borrow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer borrow.Close()
	if _, err := barrier.EnterMutation(context.Background(), borrow); !errors.Is(err, ErrWrongTurn) {
		t.Fatal(err)
	}
	if _, immediate := barrier.Seal(); !immediate {
		t.Fatal("finished ordinary calls delayed completion")
	}
}
