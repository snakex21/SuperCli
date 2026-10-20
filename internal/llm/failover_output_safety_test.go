package llm

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestFailoverOutputGuardIncludesEveryRouterSignal(t *testing.T) {
	for name, signal := range map[string]Delta{
		"content":         {Content: "partial"},
		"reasoning":       {Reasoning: "partial reasoning"},
		"native":          {NativeReasoning: &ReasoningBlock{}},
		"tool":            {ToolCall: &ToolCall{ID: "fixture", Name: "fixture_mutation"}},
		"tool-start":      {OutputStarted: true},
		"reasoning-start": {ReasoningStarted: true},
	} {
		for _, sameFrame := range []bool{false, true} {
			frameName := "later-error"
			if sameFrame {
				frameName = "same-frame-error"
			}
			t.Run(name+"/"+frameName, func(t *testing.T) {
				cause := errors.New("fixture failure after output")
				first := signal
				deltas := []Delta{first, {Err: cause}}
				if sameFrame {
					first.Err = cause
					deltas = []Delta{first}
				}
				primary := &failoverTestProvider{name: "primary", complete: func(int) (<-chan Delta, error) { return deltaStream(deltas...), nil }}
				backup := &failoverTestProvider{name: "backup", complete: func(int) (<-chan Delta, error) { return deltaStream(Delta{Content: "duplicate"}), nil }}
				f, err := NewFailover(time.Second, nil, primary, backup)
				if err != nil {
					t.Fatal(err)
				}
				ch, err := f.Complete(context.Background(), nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				got := drainRouter(t, ch)
				if backup.calls != 0 || !reflect.DeepEqual(got, deltas) {
					t.Fatalf("backup=%d got=%+v want=%+v", backup.calls, got, deltas)
				}
			})
		}
	}
}

func TestFailoverCancellationAfterMetadataDoesNotStartBackup(t *testing.T) {
	for _, metadata := range []Delta{{Notice: "fixture waiting"}, {Usage: &Usage{Input: 10}}} {
		ctx, cancel := context.WithCancel(context.Background())
		primary := &failoverTestProvider{name: "primary", complete: func(int) (<-chan Delta, error) {
			cancel()
			return deltaStream(metadata, Delta{Err: context.Canceled}), nil
		}}
		backup := &failoverTestProvider{name: "backup", complete: func(int) (<-chan Delta, error) {
			return deltaStream(Delta{Content: "unexpected"}), nil
		}}
		f, err := NewFailover(time.Second, nil, primary, backup)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		ch, err := f.Complete(ctx, nil, nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		drainRouter(t, ch)
		cancel()
		if primary.calls != 1 || backup.calls != 0 {
			t.Fatalf("primary=%d backup=%d", primary.calls, backup.calls)
		}
	}
}

func TestFailoverCancellationDuringFallbackStartupDoesNotStartNext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	primary := &failoverTestProvider{name: "primary", complete: func(int) (<-chan Delta, error) {
		return deltaStream(Delta{Err: errors.New("fixture early failure")}), nil
	}}
	backup1 := &failoverTestProvider{name: "backup1", complete: func(int) (<-chan Delta, error) {
		cancel()
		return nil, context.Canceled
	}}
	backup2 := &failoverTestProvider{name: "backup2", complete: func(int) (<-chan Delta, error) {
		return deltaStream(Delta{Content: "unexpected"}), nil
	}}
	f, err := NewFailover(time.Second, nil, primary, backup1, backup2)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := f.Complete(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	drainRouter(t, ch)
	if primary.calls != 1 || backup1.calls != 1 || backup2.calls != 0 {
		t.Fatalf("primary=%d backup1=%d backup2=%d", primary.calls, backup1.calls, backup2.calls)
	}
}

func TestFailoverMetadataStillAllowsEarlyFailover(t *testing.T) {
	for name, metadata := range map[string]Delta{
		"notice": {Notice: "fixture waiting"},
		"usage":  {Usage: &Usage{Input: 10}},
		"role":   {Role: RoleAssistant},
	} {
		t.Run(name, func(t *testing.T) {
			primary := &failoverTestProvider{name: "primary", complete: func(int) (<-chan Delta, error) {
				return deltaStream(metadata, Delta{Err: errors.New("fixture early failure")}), nil
			}}
			backup := &failoverTestProvider{name: "backup", complete: func(int) (<-chan Delta, error) {
				return deltaStream(Delta{Content: "recovered"}), nil
			}}
			f, err := NewFailover(time.Second, nil, primary, backup)
			if err != nil {
				t.Fatal(err)
			}
			ch, err := f.Complete(context.Background(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := drainRouter(t, ch)
			if primary.calls != 1 || backup.calls != 1 || len(got) != 2 || !reflect.DeepEqual(got[0], metadata) || got[1].Content != "recovered" {
				t.Fatalf("primary=%d backup=%d deltas=%+v", primary.calls, backup.calls, got)
			}
		})
	}
}
