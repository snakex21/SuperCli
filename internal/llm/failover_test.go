package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

type failoverTestProvider struct {
	name     string
	calls    int
	complete func(int) (<-chan Delta, error)
}

func (p *failoverTestProvider) Name() string { return p.name }
func (p *failoverTestProvider) Complete(context.Context, []Message, []ToolDef) (<-chan Delta, error) {
	p.calls++
	return p.complete(p.calls)
}
func deltaStream(deltas ...Delta) <-chan Delta {
	ch := make(chan Delta, len(deltas))
	for _, d := range deltas {
		ch <- d
	}
	close(ch)
	return ch
}

func TestFailoverSwitchesOnlyBeforeOutputAndUsesCooldown(t *testing.T) {
	primary := &failoverTestProvider{name: "local", complete: func(int) (<-chan Delta, error) { return deltaStream(Delta{Err: errors.New("offline")}), nil }}
	backup := &failoverTestProvider{name: "cloud", complete: func(int) (<-chan Delta, error) { return deltaStream(Delta{Content: "ok"}), nil }}
	f, err := NewFailover(time.Hour, []string{"local", "cloud"}, primary, backup)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		ch, err := f.Complete(context.Background(), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		for d := range ch {
			if d.Err != nil {
				t.Fatal(d.Err)
			}
			got += d.Content
		}
		if got != "ok" {
			t.Fatalf("%q", got)
		}
	}
	if primary.calls != 1 || backup.calls != 2 {
		t.Fatalf("calls local=%d cloud=%d", primary.calls, backup.calls)
	}
	if got := f.ActiveLabel(); got != "cloud" {
		t.Fatalf("active = %q", got)
	}
}

func TestFailoverDoesNotSwitchAfterContent(t *testing.T) {
	primary := &failoverTestProvider{name: "local", complete: func(int) (<-chan Delta, error) {
		return deltaStream(Delta{Content: "partial"}, Delta{Err: errors.New("late")}), nil
	}}
	backup := &failoverTestProvider{name: "cloud", complete: func(int) (<-chan Delta, error) { return deltaStream(Delta{Content: "wrong"}), nil }}
	f, _ := NewFailover(time.Second, nil, primary, backup)
	ch, err := f.Complete(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sawLate bool
	for d := range ch {
		if d.Err != nil {
			sawLate = true
		}
	}
	if !sawLate || backup.calls != 0 {
		t.Fatalf("late=%v backup calls=%d", sawLate, backup.calls)
	}
}

func TestFailoverDoesNotSwitchAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	primary := &failoverTestProvider{name: "local", complete: func(int) (<-chan Delta, error) {
		cancel()
		return deltaStream(Delta{Err: context.Canceled}), nil
	}}
	backup := &failoverTestProvider{name: "cloud", complete: func(int) (<-chan Delta, error) {
		return deltaStream(Delta{Content: "unexpected"}), nil
	}}
	f, _ := NewFailover(time.Second, nil, primary, backup)
	ch, err := f.Complete(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if backup.calls != 0 {
		t.Fatalf("cancellation triggered %d backup calls", backup.calls)
	}
}
