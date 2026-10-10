package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"supercli/internal/tools"
)

func bindingFixture(t testing.TB) (*Manager, *Turn, *TurnBarrier) {
	t.Helper()
	manager, err := Open(t.TempDir(), t.TempDir())
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	turn, barrier := bindingFixtureTurn(t, manager)
	return manager, turn, barrier
}

func bindingFixtureTurn(t testing.TB, manager *Manager) (*Turn, *TurnBarrier) {
	t.Helper()
	turn := manager.NewTurn("synthetic-binding", "binding fixture")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := turn.Complete(ctx); err != nil {
			t.Errorf("binding fixture cleanup: %v", err)
		}
	})
	return turn, &turn.barrier
}

func TestInvocationBorrowDetachedContextAndOwnership(t *testing.T) {
	manager, turn, barrier := bindingFixture(t)
	parent, cancelParent := context.WithCancel(WithTurn(context.Background(), turn, barrier))
	borrow, err := BorrowInvocation(parent, true)
	if err != nil {
		t.Fatal(err)
	}
	defer borrow.Close()
	detached, cancelDetached := context.WithCancel(context.Background())
	defer cancelDetached()
	detached = borrow.Bind(detached)
	inner, err := BorrowInvocation(detached, true)
	if err != nil {
		t.Fatal(err)
	}
	inner.Close() // Central runner must not release the async owner's token.
	cancelParent()
	if detached.Err() != nil {
		t.Fatal("Bind copied parent cancellation")
	}
	ready, immediate := barrier.Seal()
	if immediate {
		t.Fatal("non-owning nested handle closed owner borrow")
	}
	selected, leave, err := EnterBoundMutation(detached, manager, nil, nil)
	if err != nil || selected != turn {
		t.Fatalf("accepted detached invocation lost its turn: %v", err)
	}
	leave()
	borrow.Close()
	borrow.Close()
	<-ready
	if _, err := BorrowInvocation(detached, true); !errors.Is(err, ErrTurnSealed) {
		t.Fatalf("closed invocation was silently reopened: %v", err)
	}
	cancelDetached()
	if !errors.Is(detached.Err(), context.Canceled) {
		t.Fatal("Bind lost worker cancellation")
	}
}

func TestReadonlyInvocationDoesNotDelayAndStillRejectsLateMutation(t *testing.T) {
	manager, turn, barrier := bindingFixture(t)
	borrow, err := BorrowInvocation(WithTurn(context.Background(), turn, barrier), false)
	if err != nil {
		t.Fatal(err)
	}
	defer borrow.Close()
	ctx := borrow.Bind(context.Background())
	ready, immediate := barrier.Seal()
	if !immediate {
		t.Fatal("readonly worker delayed parent completion")
	}
	<-ready
	if _, _, err := EnterBoundMutation(ctx, manager, turn, barrier); !errors.Is(err, ErrTurnSealed) {
		t.Fatalf("unexpected late mutation escaped readonly invocation: %v", err)
	}
}

func TestCopiedFnRebindsContinuationToCurrentTurn(t *testing.T) {
	manager, oldTurn, oldBarrier := bindingFixture(t)
	newTurn, newBarrier := bindingFixtureTurn(t, manager)
	oldBarrier.Seal()
	source, worker := tools.NewRegistry(), tools.NewRegistry()
	var captured *Turn
	fnCalls := 0
	source.MustRegister(tools.Tool{
		Name: "create_file", Description: "synthetic mutator", Schema: `{"type":"object"}`,
		Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
			selected, leave, err := EnterBoundMutation(ctx, manager, oldTurn, oldBarrier)
			if err != nil {
				return tools.Result{Err: err}, nil
			}
			defer leave()
			captured = selected // Stand-in ensureBefore uses the selected Turn.
			fnCalls++
			return tools.Result{Text: "synthetic change"}, nil
		},
	})
	if err := worker.RegisterFrom(source, "create_file"); err != nil {
		t.Fatal(err)
	}
	res, err := worker.Execute(context.Background(), "create_file", json.RawMessage(`{}`))
	if err != nil || !errors.Is(res.Err, ErrTurnSealed) || fnCalls != 0 {
		t.Fatalf("unbound retained Fn changed the closed turn: result=%v err=%v", res.Err, err)
	}
	borrow, err := BorrowInvocation(WithTurn(context.Background(), newTurn, newBarrier), true)
	if err != nil {
		t.Fatal(err)
	}
	defer borrow.Close()
	res, err = worker.Execute(borrow.Bind(context.Background()), "create_file", json.RawMessage(`{}`))
	if err != nil || res.Err != nil || captured != newTurn || fnCalls != 1 {
		t.Fatalf("copied Fn ignored current continuation: result=%v err=%v", res.Err, err)
	}
}

func TestBoundMutationRejectsWrongWorkspaceAndInvalidBinding(t *testing.T) {
	manager, turn, barrier := bindingFixture(t)
	other := &Manager{home: filepath.Join("synthetic", "other-workspace")}
	otherTurn := &Turn{manager: other}
	otherBarrier := &TurnBarrier{}
	ctx := WithTurn(context.Background(), otherTurn, otherBarrier)
	if _, _, err := EnterBoundMutation(ctx, manager, turn, barrier); !errors.Is(err, ErrWrongWorkspace) {
		t.Fatalf("retained tool was rebound across homes: %v", err)
	}
	invalid := WithTurn(context.Background(), nil, nil)
	if _, _, err := EnterBoundMutation(invalid, manager, turn, barrier); !errors.Is(err, ErrMissingTurnBinding) {
		t.Fatalf("invalid explicit binding silently used lexical fallback: %v", err)
	}
	if _, err := BorrowInvocation(invalid, true); !errors.Is(err, ErrMissingTurnBinding) {
		t.Fatal(err)
	}
	if _, _, err := EnterBoundMutation(context.Background(), manager, nil, nil); !errors.Is(err, ErrMissingTurnBinding) {
		t.Fatalf("missing active controller turn allowed a mutation: %v", err)
	}
}

func TestWithTurnCreatesFreshContinuationBinding(t *testing.T) {
	manager, turn, barrier := bindingFixture(t)
	first, err := BorrowInvocation(WithTurn(context.Background(), turn, barrier), true)
	if err != nil {
		t.Fatal(err)
	}
	firstCtx := first.Bind(context.Background())
	first.Close()
	nextTurn, nextBarrier := bindingFixtureTurn(t, manager)
	nextCtx := WithTurn(firstCtx, nextTurn, nextBarrier)
	next, err := BorrowInvocation(nextCtx, true)
	if err != nil {
		t.Fatalf("fresh continuation retained closed borrow: %v", err)
	}
	defer next.Close()
	selected, leave, err := EnterBoundMutation(next.Bind(context.Background()), manager, turn, barrier)
	if err != nil || selected != nextTurn {
		t.Fatal(err)
	}
	leave()
}

func TestBorrowInvocationAbsentOrCanceled(t *testing.T) {
	borrow, err := BorrowInvocation(context.Background(), true)
	if err != nil || borrow != nil {
		t.Fatalf("standalone worker gained a checkpoint lease: %v", err)
	}
	borrow.Close()
	if borrow.Bind(context.Background()).Err() != nil {
		t.Fatal("nil binding changed context")
	}
	_, turn, barrier := bindingFixture(t)
	ctx, cancel := context.WithCancel(WithTurn(context.Background(), turn, barrier))
	cancel()
	if _, err := BorrowInvocation(ctx, true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, immediate := barrier.Seal(); !immediate {
		t.Fatal("canceled admission leaked a borrow")
	}
}

func TestHasCheckpointMutatorsIncludesDormantCapabilities(t *testing.T) {
	registry := tools.NewRegistry()
	registry.MustRegister(tools.NewReadLines("synthetic").Spec())
	registry.MustRegister(tools.NewScratchpad("synthetic").Spec())
	registry.MustRegister(tools.NewToolSearcher(registry, nil).Spec())
	if HasCheckpointMutators(registry) {
		t.Fatal("reader/discovery/excluded scratchpad demanded checkpoint borrow")
	}
	registry.MustRegister(tools.NewPatchFile("synthetic").Spec())
	if !HasCheckpointMutators(registry) {
		t.Fatal("dormant patch_file was treated as unavailable to worker")
	}
	zip := tools.NewRegistry()
	zip.MustRegister(tools.NewReadZip("synthetic", 0).Spec())
	if !HasCheckpointMutators(zip) || !HasCheckpointMutators(nil) {
		t.Fatal("potential extraction or unknown registry bypassed barrier")
	}
}

func BenchmarkCheckpointTurnBarrierBorrow(b *testing.B) {
	parent := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		barrier := &TurnBarrier{}
		borrow, err := barrier.Borrow(parent)
		if err != nil {
			b.Fatal(err)
		}
		leave, err := barrier.EnterMutation(parent, borrow)
		if err != nil {
			b.Fatal(err)
		}
		leave()
		borrow.Close()
		if _, immediate := barrier.Seal(); !immediate {
			b.Fatal("completed invocation not immediately ready")
		}
	}
}

func BenchmarkCheckpointInvocationBorrowLeaseReuse(b *testing.B) {
	manager, turn, barrier := bindingFixture(b)
	parent := WithTurn(context.Background(), turn, barrier)
	first, err := BorrowInvocation(parent, true)
	if err != nil {
		b.Fatal(err)
	}
	first.Close() // Acquire the native owner once; measure reuse separately.
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		borrow, err := BorrowInvocation(parent, true)
		if err != nil {
			b.Fatal(err)
		}
		_, leave, err := EnterBoundMutation(borrow.Bind(context.Background()), manager, turn, barrier)
		if err != nil {
			borrow.Close()
			b.Fatal(err)
		}
		leave()
		borrow.Close()
	}
	b.StopTimer()
}
