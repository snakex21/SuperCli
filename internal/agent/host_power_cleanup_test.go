package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
	toolhostpower "supercli/internal/tools/hostpower"
)

type hostPowerCleanupWriter struct {
	entered    chan struct{}
	release    chan struct{}
	projection bool
	once       sync.Once
}

func (*hostPowerCleanupWriter) AppendMessage(context.Context, llm.Message) error { return nil }
func (*hostPowerCleanupWriter) UpdateUsage(int, int) error                       { return nil }
func (w *hostPowerCleanupWriter) SaveContextProjection(ctx context.Context, _ []llm.Message) error {
	if !w.projection {
		return nil
	}
	return w.wait(ctx)
}
func (*hostPowerCleanupWriter) ReadDiscoveredTools(context.Context) ([]string, error) {
	return nil, nil
}
func (w *hostPowerCleanupWriter) SaveDiscoveredTools(ctx context.Context, _ []string) error {
	if w.projection {
		return nil
	}
	return w.wait(ctx)
}
func (w *hostPowerCleanupWriter) wait(ctx context.Context) error {
	w.once.Do(func() { close(w.entered) })
	select {
	case <-w.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestHostPowerDoneWaitsForCleanup(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "external_racing_run_end"}[external], func(t *testing.T) {
			controller, clock := newHostPowerTestController(t)
			svc, id := hostPowerTestGoals(t)
			approvalCtx, _ := hostPowerTestApproval(t)
			ctx, cancel := context.WithTimeout(approvalCtx, 5*time.Second)
			defer cancel()
			writer := &hostPowerCleanupWriter{entered: make(chan struct{}), release: make(chan struct{})}
			released := false
			defer func() {
				if !released {
					close(writer.release)
				}
			}()
			p := &hostPowerTestProvider{}
			registry := tools.NewRegistry()
			registry.MustRegister(tools.Tool{Name: "fixture_cleanup", Description: "Fixture discovery for cleanup persistence", Schema: `{}`, ReadOnly: true, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
			l := makeLoop(t, p, registry, "system")
			l.writer = writer
			l.ConfigureHostPower(controller, svc, external)
			p.hook = func(ctx context.Context) error {
				// Prove the configured primary received the actual tool binding.
				status, err := toolhostpower.Spec().Fn(ctx, json.RawMessage(`{"action":"status"}`))
				if err != nil {
					return err
				}
				if status.Err != nil {
					return status.Err
				}
				if err := armHostPowerTest(ctx, svc, id); err != nil {
					return err
				}
				completeHostPowerTestGoal(t, svc, id)
				// Projection persistence may legitimately precede DoneEvent.
				// Discovery is persisted only in the final cleanup defer.
				registry.ActivateDiscovered("fixture_cleanup")
				return nil
			}
			ch, err := l.Run(ctx, "Do the work then shut down")
			if err != nil {
				t.Fatal(err)
			}
			done := false
			for event := range ch {
				if _, ok := event.(DoneEvent); ok {
					done = true
					break
				}
			}
			if !done {
				t.Fatal("no DoneEvent")
			}
			select {
			case <-writer.entered:
			case <-ctx.Done():
				t.Fatal("cleanup writer did not start after DoneEvent")
			}
			if external {
				l.FinalizeHostPower(nil)
			}
			if controller.Snapshot().Status != "armed" || clock.timers() != 0 {
				t.Fatal("Done/finalizer bypassed cleanup")
			}
			if _, err := l.Run(ctx, "racing next run"); err == nil {
				t.Fatal("run reused before cleanup")
			}
			close(writer.release)
			released = true
			drainEvents(t, ch)
			if state := controller.Snapshot(); state.Status != "countdown" {
				t.Fatalf("cleanup-complete state = %+v", state)
			}
			if clock.timers() != 1 {
				t.Fatal("not finalized exactly once")
			}
		})
	}
}

func TestHostPowerCompletionWaitsForProjection(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "external_racing_projection"}[external], func(t *testing.T) {
			controller, clock := newHostPowerTestController(t)
			svc, id := hostPowerTestGoals(t)
			approvalCtx, _ := hostPowerTestApproval(t)
			ctx, cancel := context.WithTimeout(approvalCtx, 5*time.Second)
			defer cancel()
			writer := &hostPowerCleanupWriter{entered: make(chan struct{}), release: make(chan struct{}), projection: true}
			var release sync.Once
			defer release.Do(func() { close(writer.release) })
			p := &hostPowerTestProvider{}
			l := makeLoop(t, p, tools.NewRegistry(), "system")
			l.writer = writer
			l.ConfigureHostPower(controller, svc, external)
			p.hook = func(ctx context.Context) error {
				if err := armHostPowerTest(ctx, svc, id); err != nil {
					return err
				}
				completeHostPowerTestGoal(t, svc, id)
				return nil
			}
			ch, err := l.Run(ctx, "Complete the temporary fixture goal")
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-writer.entered:
			case <-ctx.Done():
				t.Fatal("required projection was not persisted before completion")
			}
			if external {
				l.FinalizeHostPower(nil)
			}
		drainPending:
			for {
				select {
				case event, open := <-ch:
					if !open {
						t.Fatal("run ended before required projection persistence")
					}
					if _, done := event.(DoneEvent); done {
						t.Fatal("DoneEvent bypassed projection persistence")
					}
				default:
					break drainPending
				}
			}
			if controller.Snapshot().Status != "armed" || clock.timers() != 0 {
				t.Fatal("projection/finalizer bypassed persistence")
			}
			release.Do(func() { close(writer.release) })
			foundDone := false
			for _, event := range drainEvents(t, ch) {
				if _, ok := event.(DoneEvent); ok {
					foundDone = true
				}
			}
			if !foundDone || controller.Snapshot().Status != "countdown" || clock.timers() != 1 {
				t.Fatal("durable completion did not finalize exactly once")
			}
		})
	}
}

func TestHostPowerWaitsForBackgroundWork(t *testing.T) {
	controller, clock := newHostPowerTestController(t)
	svc, id := hostPowerTestGoals(t)
	ctx, _ := hostPowerTestApproval(t)
	var finish func()
	p := &hostPowerTestProvider{hook: func(ctx context.Context) error {
		if err := armHostPowerTest(ctx, svc, id); err != nil {
			return err
		}
		completeHostPowerTestGoal(t, svc, id)
		finish = backgroundRunFrom(ctx).start()
		return nil
	}}
	l := makeLoop(t, p, tools.NewRegistry(), "system")
	l.ConfigureHostPower(controller, svc, false)
	ch, err := l.Run(ctx, "Finish all background work then power off")
	if err != nil {
		t.Fatal(err)
	}
	waiting := false
	for event := range ch {
		if event, ok := event.(DelegatedWaitEvent); ok && event.Waiting {
			waiting = true
			break
		}
	}
	if !waiting {
		t.Fatal("did not wait for background work")
	}
	if controller.Snapshot().Status != "armed" || clock.timers() != 0 {
		t.Fatal("pending worker permitted countdown")
	}
	finish()
	drainEvents(t, ch)
	if state := controller.Snapshot(); state.Status != "countdown" {
		t.Fatalf("state = %+v", state)
	}
}
