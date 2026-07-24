package tui

import (
	"context"
	"testing"
)

func TestCancelState_NewIsIdle(t *testing.T) {
	cs := NewCancelState()
	if cs.IsArmed() {
		t.Fatal("new CancelState should not be armed")
	}
	if cs.Scope() != cancelNothing {
		t.Fatal("scope should be cancelNothing")
	}
	if cs.Cancel() {
		t.Fatal("cancel on idle should return false")
	}
}

func TestCancelState_Arm(t *testing.T) {
	cs := NewCancelState()
	ctx, cancel := context.WithCancel(context.Background())
	_ = ctx
	cs.Arm(cancelRun, cancel)
	if !cs.IsArmed() {
		t.Fatal("should be armed after Arm")
	}
	if cs.Scope() != cancelRun {
		t.Fatalf("scope = %d, want cancelRun", cs.Scope())
	}
}

func TestCancelState_Cancel(t *testing.T) {
	cs := NewCancelState()
	ctx, cancel := context.WithCancel(context.Background())
	cs.Arm(cancelRun, cancel)
	if !cs.Cancel() {
		t.Fatal("Cancel should return true")
	}
	if ctx.Err() == nil {
		t.Fatal("context should be cancelled")
	}
}

func TestCancelState_Disarm(t *testing.T) {
	cs := NewCancelState()
	_, cancel := context.WithCancel(context.Background())
	cs.Arm(cancelToolCall, cancel)
	cs.Disarm()
	if cs.IsArmed() {
		t.Fatal("should not be armed after Disarm")
	}
	if cs.Cancel() {
		t.Fatal("cancel after disarm should return false")
	}
}

func TestCancelState_ScopeToolCall(t *testing.T) {
	cs := NewCancelState()
	_, cancel := context.WithCancel(context.Background())
	cs.Arm(cancelToolCall, cancel)
	if cs.Scope() != cancelToolCall {
		t.Fatalf("scope = %d, want cancelToolCall", cs.Scope())
	}
}
