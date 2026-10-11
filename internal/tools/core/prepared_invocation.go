package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// PreparedInvocation holds one accepted tool call. Its registry identity and
// argument bytes are private; it is not a result cache or an authorization grant.
// Registered contracts are append-only, so later discovery or registrations do
// not invalidate this call's schema. The execution callback is looked up afresh.
type PreparedInvocation struct {
	owner   *Registry
	name    string
	args    string
	argsNil bool
}

var errInvalidPreparedInvocation = errors.New("invalid prepared tool invocation")

// PrepareInvocation applies the same coercion, validation and pure repair as
// Execute, without invoking the tool. The returned accepted arguments may alias
// raw, but are separate from the immutable token; changing them or raw for cache
// keys or checked replay cannot change the invocation's accepted arguments.
func (r *Registry) PrepareInvocation(name string, raw json.RawMessage) (PreparedInvocation, json.RawMessage, error) {
	prepared, err := r.PrepareArgs(name, raw)
	if err != nil {
		return PreparedInvocation{}, nil, err
	}
	invocation := PreparedInvocation{
		owner:   r,
		name:    name,
		args:    string(prepared),
		argsNil: prepared == nil,
	}
	return invocation, prepared, nil
}

// ExecutePrepared skips only the already completed argument preparation. It
// invokes the current registered callback with the caller's current context, so
// live permissions, checkpoint binding and tool errors still apply. Like Execute,
// it does not check visibility or add a cancellation policy before the callback;
// those dispatch checks remain the agent's responsibility.
func (r *Registry) ExecutePrepared(ctx context.Context, invocation PreparedInvocation) (Result, error) {
	if invocation.owner == nil || invocation.owner != r || invocation.name == "" {
		return Result{Err: errInvalidPreparedInvocation}, errInvalidPreparedInvocation
	}
	r.mu.RLock()
	t, ok := r.tools[invocation.name]
	r.mu.RUnlock()
	if !ok {
		err := fmt.Errorf("%w: %q", ErrUnknownTool, invocation.name)
		return Result{Err: err}, err
	}
	// A callback may mutate its RawMessage. Keep that mutable execution buffer
	// separate from the accepted token and the arguments used for replay keys.
	var args json.RawMessage
	if !invocation.argsNil {
		args = json.RawMessage(invocation.args)
	}
	return t.Fn(ctx, args)
}
