package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/goal"
	"supercli/internal/tools"
	toolhostpower "supercli/internal/tools/hostpower"
)

// These scripts cross the actual agent tool-dispatch boundary. host_power is
// the production Spec, bound by Loop.Run, and only the work tool is a fixture.
// Eligibility and approval are never substituted. Execution/timing alone are
// inert, manually delivered test seams.
func hostPowerIntegrationScripts(goalID string) [][]llm.Delta {
	args, _ := json.Marshal(map[string]string{"action": "arm", "goal_id": goalID})
	return [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "arm", Name: "host_power", Arguments: string(args)}}, {FinishReason: "tool_calls"}},
		{{ToolCall: &llm.ToolCall{ID: "work", Name: "fixture_work", Arguments: `{}`}}, {FinishReason: "tool_calls"}},
		{{Role: llm.RoleAssistant, Content: "The requested work result is ready."}, {FinishReason: "stop"}},
	}
}

func assertHostPowerIntegrationTools(t *testing.T, events []Event, worker bool, workFailed bool) {
	t.Helper()
	armCalls, armResults, workResults, done := 0, 0, 0, 0
	for _, event := range events {
		switch event := event.(type) {
		case ToolCallEvent:
			if event.Name == "host_power" {
				armCalls++
			}
		case ToolResultEvent:
			if event.ID == "arm" {
				armResults++
				if worker != (event.Err != nil) {
					t.Fatalf("arm result error = %v, worker=%v", event.Err, worker)
				}
			}
			if event.ID == "work" {
				workResults++
				if workFailed != (event.Err != nil) {
					t.Fatalf("work result error = %v, want failed=%v", event.Err, workFailed)
				}
			}
		case DoneEvent:
			done++
		case ErrorEvent:
			t.Fatalf("unexpected run error: %v", event.Err)
		}
	}
	if armCalls != 1 || armResults != 1 || workResults != 1 || done != 1 {
		t.Fatalf("tool-dispatch counts arm=%d results=%d work=%d done=%d", armCalls, armResults, workResults, done)
	}
}

func TestHostPowerIntegrationToolDispatch(t *testing.T) {
	for _, kind := range []string{"success", "incomplete", "failed_verification", "tool_failure", "persistence_failure", "external_success", "external_failure", "deadline_recheck", "worker"} {
		t.Run(kind, func(t *testing.T) {
			controller, clock := newHostPowerTestController(t)
			svc, id := hostPowerTestGoals(t)
			ctx, approvals := hostPowerTestApproval(t)
			registry := tools.NewRegistry()
			registry.MustRegister(toolhostpower.Spec())
			registry.MustRegister(tools.Tool{
				Name: "fixture_work", Description: "Record inert fixture work and its verification in the temporary goal DB", Schema: `{"type":"object"}`,
				Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
					if kind == "incomplete" {
						return tools.Result{Text: "Work is incomplete; tasks remain open."}, nil
					}
					if kind == "failed_verification" {
						if err := svc.SetTaskStatus(ctx, id, 1, goal.TaskDone); err != nil {
							return tools.Result{}, err
						}
						if err := svc.Verify(ctx, id, false, "Fixture verification failed"); err != nil {
							return tools.Result{}, err
						}
						return tools.Result{Text: "Verification failed; goal is not complete."}, nil
					}
					completeHostPowerTestGoal(t, svc, id)
					if kind == "tool_failure" {
						return tools.Result{Err: errors.New("concrete fixture failure despite completed goal")}, nil
					}
					return tools.Result{Text: "Fixture work and verification completed."}, nil
				},
			})
			provider := &stubProvider{name: "host-power-integration", scripts: hostPowerIntegrationScripts(id)}
			loop := makeLoop(t, provider, registry, "Use the requested tools.")
			external := kind == "external_success" || kind == "external_failure"
			if kind == "worker" {
				// Even a real binding inherited from a configured parent's active
				// invocation cannot authorize a NewLoop worker's tool dispatch.
				parent := makeLoop(t, &hostPowerTestProvider{}, tools.NewRegistry(), "parent")
				parent.ConfigureHostPower(controller, svc, false)
				var err error
				ctx, err = parent.beginHostPower(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer parent.endHostPower(ctx)
			} else {
				loop.ConfigureHostPower(controller, svc, external)
			}
			if kind == "persistence_failure" {
				loop.writer = &flakyWriter{failAppends: 1000}
			}
			events := runHostPowerTest(t, loop, ctx)
			assertHostPowerIntegrationTools(t, events, kind == "worker", kind == "tool_failure")
			wantApprovals := int32(1)
			if kind == "worker" {
				wantApprovals = 0
			}
			if approvals.Load() != wantApprovals {
				t.Fatalf("trusted UI confirmations = %d, want %d", approvals.Load(), wantApprovals)
			}
			if external {
				if state := controller.Snapshot(); state.Status != "armed" || clock.timers() != 0 {
					t.Fatalf("external cleanup fence bypassed: %+v", state)
				}
				if _, err := loop.Run(ctx, "next work"); err == nil {
					t.Fatal("run reused before external cleanup acknowledgement")
				}
				var finalErr error
				if kind == "external_failure" {
					finalErr = errors.New("frontend persistence failed")
				}
				var wg sync.WaitGroup
				for i := 0; i < 8; i++ {
					wg.Add(1)
					go func() { defer wg.Done(); loop.FinalizeHostPower(finalErr) }()
				}
				wg.Wait()
			}
			want := "blocked"
			if kind == "success" || kind == "external_success" || kind == "deadline_recheck" {
				want = "countdown"
			}
			if kind == "worker" {
				want = "idle"
			}
			if state := controller.Snapshot(); state.Status != want {
				t.Fatalf("state = %+v, want %s", state, want)
			}
			if clock.executions.Load() != 0 {
				t.Fatal("execution occurred without delivering the fake deadline")
			}
			if kind == "deadline_recheck" {
				if err := svc.SetStatus(context.Background(), id, goal.StatusActive); err != nil {
					t.Fatal(err)
				}
			}
			clock.fire()
			clock.fire()
			wantExec := int32(0)
			if kind == "success" || kind == "external_success" {
				wantExec = 1
			}
			if clock.executions.Load() != wantExec {
				t.Fatalf("fake executions = %d, want %d", clock.executions.Load(), wantExec)
			}
			if kind == "deadline_recheck" && controller.Snapshot().Status != "blocked" {
				t.Fatal("deadline did not re-read changed goal verification")
			}
			wantTimers := 0
			if want == "countdown" {
				wantTimers = 1
			}
			if clock.timers() != wantTimers {
				t.Fatalf("scheduled timers = %d, want %d", clock.timers(), wantTimers)
			}
		})
	}
}

// Discovery is persisted only in the run's cleanup defer, unlike projections
// which can also be retried at a model-step boundary.
type hostPowerIntegrationDiscoveryWriter struct {
	entered chan struct{}
	release chan struct{}
}

func (*hostPowerIntegrationDiscoveryWriter) AppendMessage(context.Context, llm.Message) error {
	return nil
}
func (*hostPowerIntegrationDiscoveryWriter) UpdateUsage(int, int) error { return nil }
func (*hostPowerIntegrationDiscoveryWriter) ReadDiscoveredTools(context.Context) ([]string, error) {
	return nil, nil
}
func (w *hostPowerIntegrationDiscoveryWriter) SaveDiscoveredTools(ctx context.Context, _ []string) error {
	close(w.entered)
	select {
	case <-w.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestHostPowerIntegrationDoneCleanupFence(t *testing.T) {
	controller, clock := newHostPowerTestController(t)
	svc, id := hostPowerTestGoals(t)
	ctx, approvals := hostPowerTestApproval(t)
	writer := &hostPowerIntegrationDiscoveryWriter{entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(writer.release) })
	registry := tools.NewRegistry()
	registry.MustRegister(toolhostpower.Spec())
	registry.MustRegister(tools.Tool{Name: "fixture_work", Description: "Complete temporary goal and request a cleanup discovery write", Schema: `{"type":"object"}`, Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
		completeHostPowerTestGoal(t, svc, id)
		registry.ActivateDiscovered("host_power")
		return tools.Result{Text: "Fixture complete."}, nil
	}})
	loop := makeLoop(t, &stubProvider{name: "host-power-integration", scripts: hostPowerIntegrationScripts(id)}, registry, "system")
	loop.writer = writer
	loop.ConfigureHostPower(controller, svc, true)
	ch, err := loop.Run(ctx, "Finish the work, then power off.")
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	for event := range ch {
		events = append(events, event)
		if _, ok := event.(DoneEvent); ok {
			break
		}
	}
	<-writer.entered
	loop.FinalizeHostPower(nil)
	if state := controller.Snapshot(); state.Status != "armed" || clock.timers() != 0 {
		t.Fatalf("early acknowledgement bypassed cleanup: %+v", state)
	}
	release.Do(func() { close(writer.release) })
	events = append(events, drainEvents(t, ch)...)
	assertHostPowerIntegrationTools(t, events, false, false)
	if approvals.Load() != 1 || controller.Snapshot().Status != "countdown" || clock.timers() != 1 {
		t.Fatalf("cleanup did not finalize exactly once: %+v confirmations=%d timers=%d", controller.Snapshot(), approvals.Load(), clock.timers())
	}
	clock.fire()
	clock.fire()
	if clock.executions.Load() != 1 {
		t.Fatal("successful cleanup did not permit exactly one fake execution")
	}
}
