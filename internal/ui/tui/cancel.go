package tui

import (
	"context"
)

// cancelScope identifies what Ctrl+C should cancel.
type cancelScope int

const (
	cancelNothing cancelScope = iota // not running
	cancelToolCall                  // running a tool → cancel current tool
	cancelRun                       // running agent → cancel entire run
)

// CancelState manages the Ctrl+C context for an active agent run.
// When a run starts, CancelState arms a context and CancelFunc.
// Ctrl+C calls the CancelFunc; the loop respects ctx.Done().
// After cancellation, the state resets to cancelNothing.
type CancelState struct {
	scope  cancelScope
	cancel context.CancelFunc
}

// NewCancelState returns an idle CancelState.
func NewCancelState() CancelState {
	return CancelState{scope: cancelNothing}
}

// Arm sets the cancel scope and function when a run starts.
// This is called from handleKey when the user presses Enter
// to submit a prompt.
func (cs *CancelState) Arm(scope cancelScope, cancel context.CancelFunc) {
	cs.scope = scope
	cs.cancel = cancel
}

// Disarm clears the cancel state. Called when DoneEvent or
// ErrorEvent arrives, or after slash command completes.
func (cs *CancelState) Disarm() {
	cs.scope = cancelNothing
	cs.cancel = nil
}

// Cancel calls the underlying CancelFunc if armed. Returns
// true if a cancellation was actually triggered.
func (cs *CancelState) Cancel() bool {
	if cs.cancel != nil {
		cs.cancel()
		return true
	}
	return false
}

// Scope returns the current cancellation scope.
func (cs CancelState) Scope() cancelScope {
	return cs.scope
}

// IsArmed returns true when there is an active run.
func (cs CancelState) IsArmed() bool {
	return cs.scope != cancelNothing
}
