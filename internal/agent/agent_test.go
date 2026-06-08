package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewNoop_RejectsEmptyName(t *testing.T) {
	if _, err := NewNoop(""); err == nil {
		t.Fatal("expected error on empty name")
	}
}

func TestNewNoop_StoresName(t *testing.T) {
	a, err := NewNoop("planner")
	if err != nil {
		t.Fatalf("NewNoop: %v", err)
	}
	if a.Name() != "planner" {
		t.Fatalf("Name = %q, want planner", a.Name())
	}
}

func TestNoop_Run_RejectsEmptyPrompt(t *testing.T) {
	a, _ := NewNoop("planner")
	if _, err := a.Run(context.Background(), ""); err == nil {
		t.Fatal("expected error on empty prompt")
	}
}

func TestNoop_Run_EmitsDone(t *testing.T) {
	a, _ := NewNoop("planner")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ch, err := a.Run(ctx, "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var got Event
	select {
	case got = <-ch:
	case <-ctx.Done():
		t.Fatal("timeout waiting for event")
	}
	done, ok := got.(DoneEvent)
	if !ok {
		t.Fatalf("first event = %T, want DoneEvent", got)
	}
	if done.Usage.Total != 0 {
		t.Fatalf("noop usage = %+v, want zero", done.Usage)
	}
	// channel should close right after
	select {
	case _, open := <-ch:
		if open {
			t.Fatal("expected channel close, got another event")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("channel did not close")
	}
}

func TestNoop_Run_RespectsContextCancel(t *testing.T) {
	a, _ := NewNoop("planner")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	ch, err := a.Run(ctx, "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var got Event
	select {
	case got = <-ch:
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
	ev, ok := got.(ErrorEvent)
	if !ok {
		t.Fatalf("event = %T, want ErrorEvent", got)
	}
	if !errors.Is(ev.Err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", ev.Err)
	}
}

func TestUsage_ZeroIsUnknown(t *testing.T) {
	u := Usage{}
	if u.Input != 0 || u.Output != 0 || u.Total != 0 {
		t.Fatalf("expected zero usage, got %+v", u)
	}
}
