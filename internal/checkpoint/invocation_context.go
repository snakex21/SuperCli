package checkpoint

import (
	"context"
	"errors"
	"path/filepath"

	tools "supercli/internal/tools/core"
)

var (
	ErrMissingTurnBinding = errors.New("checkpoint mutation requires an active turn binding")
	ErrWrongWorkspace     = errors.New("checkpoint worker binding belongs to another workspace")
)

type turnBinding struct {
	turn    *Turn
	barrier *TurnBarrier
	borrow  *TurnBorrow
}

type turnBindingKey struct{}

// WithTurn binds a fresh parent invocation. It deliberately drops any old
// borrow: a continuation must obtain a token for its current run.
func WithTurn(ctx context.Context, turn *Turn, barrier *TurnBarrier) context.Context {
	return context.WithValue(ctx, turnBindingKey{}, turnBinding{turn: turn, barrier: barrier})
}

// InvocationBorrow carries only checkpoint identity, not the parent context,
// event sink, worker Loop or Registry. The owning handle closes before background
// result persistence/notification, once runWorkerLoop has fully drained.
type InvocationBorrow struct {
	binding turnBinding
	owns    bool
}

// BorrowInvocation takes a token only for a checkpoint-mutating-capable worker.
// Read-only workers still carry the current identity without delaying Complete.
// A token already present in ctx is reused with a non-owning handle: async accepts
// before go, and the central worker runner can call the same API safely.
// No binding means checkpointing is not configured (standalone worker callers).
func BorrowInvocation(ctx context.Context, mutatingCapable bool) (*InvocationBorrow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binding, ok := ctx.Value(turnBindingKey{}).(turnBinding)
	if !ok {
		return nil, nil
	}
	if binding.turn == nil || binding.turn.manager == nil || binding.barrier == nil {
		return nil, ErrMissingTurnBinding
	}
	if !mutatingCapable {
		binding.borrow = nil
		return &InvocationBorrow{binding: binding}, nil
	}
	if binding.borrow != nil {
		if err := binding.barrier.checkBorrow(binding.borrow); err != nil {
			return nil, err
		}
		return &InvocationBorrow{binding: binding}, nil
	}
	borrow, err := binding.barrier.Borrow(ctx)
	if err != nil {
		return nil, err
	}
	if err := binding.turn.ensureLifetimeLease(ctx); err != nil {
		borrow.Close()
		return nil, err
	}
	binding.borrow = borrow
	return &InvocationBorrow{binding: binding, owns: true}, nil
}

// Bind copies identity to a detached worker context while preserving that
// context's own cancellation/deadline. It retains no parent context chain.
func (borrow *InvocationBorrow) Bind(ctx context.Context) context.Context {
	if borrow == nil {
		return ctx
	}
	return context.WithValue(ctx, turnBindingKey{}, borrow.binding)
}

func (borrow *InvocationBorrow) Close() {
	if borrow != nil && borrow.owns {
		borrow.binding.borrow.Close()
	}
}

// EnterBoundMutation selects the current invocation's Turn, not a retained
// registry's lexical Turn or Controller's newest Turn. The original tool still
// closes over its own home, so rebinding across workspaces must fail closed.
// fallback is used only when ctx has no explicit checkpoint binding.
func EnterBoundMutation(ctx context.Context, expected *Manager, fallback *Turn, fallbackBarrier *TurnBarrier) (*Turn, func(), error) {
	binding, ok := ctx.Value(turnBindingKey{}).(turnBinding)
	if !ok {
		binding = turnBinding{turn: fallback, barrier: fallbackBarrier}
	}
	if expected == nil || binding.turn == nil || binding.turn.manager == nil || binding.barrier == nil {
		return nil, nil, ErrMissingTurnBinding
	}
	if !pathEqual(filepath.Clean(expected.home), filepath.Clean(binding.turn.manager.home)) {
		return nil, nil, ErrWrongWorkspace
	}
	leave, err := binding.barrier.EnterMutation(ctx, binding.borrow)
	if err != nil {
		return nil, nil, err
	}
	if err := binding.turn.ensureLifetimeLease(ctx); err != nil {
		leave()
		return nil, nil, err
	}
	return binding.turn, leave, nil
}

// HasCheckpointMutators inspects actual registered capabilities once per worker
// invocation, including dormant tools. Discovery cannot make an unregistered
// target executable. This is checkpoint workspace scope, not a certification
// that scratchpad/discovery/external tools have no effects of their own.
func HasCheckpointMutators(registry *tools.Registry) bool {
	if registry == nil {
		return true // Unknown capabilities must not bypass the barrier.
	}
	for _, name := range registry.Names() {
		tool, ok := registry.Get(name)
		if !ok {
			return true
		}
		if !tool.ReadOnly && mutatingTool(name) {
			return true
		}
	}
	return false
}
