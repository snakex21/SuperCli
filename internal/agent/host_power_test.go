package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage"
	"supercli/internal/storage/goal"
	systemhostpower "supercli/internal/system/hostpower"
	"supercli/internal/tools"
	toolhostpower "supercli/internal/tools/hostpower"
	"supercli/internal/tools/interactive"
)

// Every test supplies an inert executor and a manually delivered clock. There
// is deliberately no reference to the production shutdown executor here.
type hostPowerTestClock struct {
	mu         sync.Mutex
	now        time.Time
	callbacks  []func()
	executions atomic.Int32
}

func newHostPowerTestController(t *testing.T) (*systemhostpower.Controller, *hostPowerTestClock) {
	t.Helper()
	clock := &hostPowerTestClock{now: time.Unix(1000, 0)}
	controller := systemhostpower.New(systemhostpower.Options{
		Execute: func(context.Context) error { clock.executions.Add(1); return nil },
		Delay:   time.Minute,
		Now:     func() time.Time { clock.mu.Lock(); defer clock.mu.Unlock(); return clock.now },
		AfterFunc: func(_ time.Duration, callback func()) func() {
			clock.mu.Lock()
			clock.callbacks = append(clock.callbacks, callback)
			clock.mu.Unlock()
			return func() {}
		},
	})
	t.Cleanup(controller.Close)
	return controller, clock
}

func (c *hostPowerTestClock) fire() {
	c.mu.Lock()
	c.now = c.now.Add(time.Minute)
	callbacks := append([]func(){}, c.callbacks...)
	c.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

func (c *hostPowerTestClock) timers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.callbacks)
}

type hostPowerTestProvider struct {
	hook func(context.Context) error
	tool bool
}

func (*hostPowerTestProvider) Name() string { return "host-power-test" }
func (p *hostPowerTestProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	if p.hook != nil {
		if err := p.hook(ctx); err != nil {
			return nil, err
		}
	}
	if p.tool {
		ch := make(chan llm.Delta, 2)
		ch <- llm.Delta{ToolCall: &llm.ToolCall{ID: "one", Name: "noop", Arguments: `{}`}}
		ch <- llm.Delta{FinishReason: "tool_calls"}
		close(ch)
		return ch, nil
	}
	ch := make(chan llm.Delta, 2)
	ch <- llm.Delta{Role: llm.RoleAssistant, Content: "Work result."}
	ch <- llm.Delta{FinishReason: "stop", Usage: &llm.Usage{Input: 1, Output: 1, Total: 2}}
	close(ch)
	return ch, nil
}

func hostPowerTestGoals(t *testing.T) (*goal.Service, string) {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := goal.NewStorage(db)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc := goal.NewService(store)
	g, err := svc.Set(context.Background(), "Finish requested work", "", "tests pass", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddTask(context.Background(), g.ID, "Implement and check"); err != nil {
		t.Fatal(err)
	}
	return svc, g.ID
}

func completeHostPowerTestGoal(t *testing.T, svc *goal.Service, id string) {
	t.Helper()
	ctx := context.Background()
	if err := svc.SetTaskStatus(ctx, id, 1, goal.TaskDone); err != nil {
		t.Fatal(err)
	}
	if err := svc.Verify(ctx, id, true, "mocked checks passed"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetStatus(ctx, id, goal.StatusDone); err != nil {
		t.Fatal(err)
	}
}

func hostPowerTestApproval(t *testing.T) (context.Context, *atomic.Int32) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	requests := make(chan interactive.AskRequest)
	count := &atomic.Int32{}
	go func() {
		for {
			select {
			case request := <-requests:
				count.Add(1)
				request.Respond <- interactive.AskAnswer{Selected: []string{"Allow once"}}
			case <-ctx.Done():
				return
			}
		}
	}()
	return interactive.WithAskChannel(ctx, requests), count
}

func armHostPowerToolTest(ctx context.Context, goalID string) error {
	args, _ := json.Marshal(map[string]string{"action": "arm", "goal_id": goalID})
	result, err := toolhostpower.Spec().Fn(ctx, args)
	if err != nil {
		return err
	}
	return result.Err
}

// All lifecycle tests use the real tool's approval and fresh-goal recheck;
// no test substitutes Controller.Arm or an eligibility closure.
func armHostPowerTest(ctx context.Context, _ *goal.Service, goalID string) error {
	return armHostPowerToolTest(ctx, goalID)
}

func runHostPowerTest(t *testing.T, l *Loop, ctx context.Context) []Event {
	t.Helper()
	ch, err := l.Run(ctx, "Finish this work, then power off the host.")
	if err != nil {
		t.Fatal(err)
	}
	return drainEvents(t, ch)
}

func TestHostPowerSuccessfulGoalAndUniqueInvocation(t *testing.T) {
	controller, clock := newHostPowerTestController(t)
	svc, id := hostPowerTestGoals(t)
	ctx, approvals := hostPowerTestApproval(t)
	var jobs []string
	p := &hostPowerTestProvider{hook: func(ctx context.Context) error {
		jobs = append(jobs, hostPowerInvocationFrom(ctx).jobID)
		if err := armHostPowerTest(ctx, svc, id); err != nil {
			return err
		}
		completeHostPowerTestGoal(t, svc, id)
		return nil
	}}
	l := makeLoop(t, p, tools.NewRegistry(), "system")
	l.ConfigureHostPower(controller, svc, false)
	runHostPowerTest(t, l, ctx)
	if state := controller.Snapshot(); state.Status != "countdown" {
		t.Fatalf("state = %+v", state)
	}
	if clock.timers() != 1 || clock.executions.Load() != 0 {
		t.Fatal("must schedule once, not execute on Done")
	}
	clock.fire()
	clock.fire()
	if clock.executions.Load() != 1 {
		t.Fatal("executor must run exactly once")
	}
	// A fresh invocation on the same constructed Loop has a different identity.
	p.hook = func(ctx context.Context) error { jobs = append(jobs, hostPowerInvocationFrom(ctx).jobID); return nil }
	runHostPowerTest(t, l, ctx)
	if len(jobs) != 2 || jobs[0] == jobs[1] {
		t.Fatalf("invocation identities = %v", jobs)
	}
	if approvals.Load() != 1 {
		t.Fatalf("confirmation count = %d", approvals.Load())
	}
}

func TestHostPowerWorkerCannotInheritAuthority(t *testing.T) {
	controller, clock := newHostPowerTestController(t)
	svc, id := hostPowerTestGoals(t)
	ctx, approvals := hostPowerTestApproval(t)
	parent := makeLoop(t, &hostPowerTestProvider{}, tools.NewRegistry(), "system")
	parent.ConfigureHostPower(controller, svc, false)
	bound, err := parent.beginHostPower(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.endHostPower(bound)
	var denied bool
	child := makeLoop(t, &hostPowerTestProvider{hook: func(ctx context.Context) error {
		if hostPowerInvocationFrom(ctx) != nil {
			t.Error("child inherited parent's completion marker")
		}
		denied = armHostPowerToolTest(ctx, id) != nil
		return nil
	}}, tools.NewRegistry(), "system")
	runHostPowerTest(t, child, bound)
	if !denied || approvals.Load() != 0 || clock.timers() != 0 {
		t.Fatal("worker inherited authority or asked for approval")
	}
	if strings.Contains(child.downloadExportContext, hostPowerInstruction) {
		t.Fatal("worker received human-loop instruction")
	}
	if state := controller.Snapshot(); state.Status != "idle" {
		t.Fatalf("worker changed parent controller: %+v", state)
	}
}

func TestHostPowerBlockedOutcomes(t *testing.T) {
	for _, kind := range []string{"incomplete_goal", "provider_error", "panic", "cancel", "failed_check", "concrete_failure", "append_failure", "projection_failure", "usage_failure", "step_limit", "pending_steering"} {
		t.Run(kind, func(t *testing.T) {
			controller, clock := newHostPowerTestController(t)
			svc, id := hostPowerTestGoals(t)
			ctx, _ := hostPowerTestApproval(t)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			p := &hostPowerTestProvider{}
			l := makeLoop(t, p, tools.NewRegistry(), "system")
			l.ConfigureHostPower(controller, svc, false)
			writer := &flakyWriter{}
			if kind == "append_failure" {
				writer.failAppends = 1000
				l.writer = writer
			}
			if kind == "projection_failure" {
				writer.failProjection = true
				l.writer = writer
			}
			if kind == "usage_failure" {
				writer.failUsage = true
				l.writer = writer
			}
			p.hook = func(ctx context.Context) error {
				if err := armHostPowerTest(ctx, svc, id); err != nil {
					return err
				}
				if kind != "incomplete_goal" {
					completeHostPowerTestGoal(t, svc, id)
				}
				switch kind {
				case "provider_error":
					return errors.New("provider failed")
				case "panic":
					panic("provider panic")
				case "cancel":
					cancel()
				case "failed_check":
					l.failedChecks.record(verificationObservation{failed: true, sequence: verificationSequence.Add(1)})
				case "concrete_failure":
					l.concreteFailure.Store(true)
				case "projection_failure":
					l.persistProjection(ctx)
				case "pending_steering":
					// At the last step it cannot be consumed, and cleanup must reject it.
					if !l.QueueInterjection("unfinished extra work") {
						t.Error("queue rejected")
					}
				}
				return nil
			}
			if kind == "step_limit" {
				l.maxSteps = 1
				p.tool = true
				l.registry.MustRegister(tools.Tool{Name: "noop", Description: "Inert test tool", Schema: `{"type":"object"}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{Text: "ok"}, nil }})
			}
			if kind == "pending_steering" {
				l.maxSteps = 1
			}
			runHostPowerTest(t, l, ctx)
			want := "blocked"
			if kind == "pending_steering" {
				want = "cancelled" // trusted human input revoked the old consent
			}
			if state := controller.Snapshot(); state.Status != want {
				t.Fatalf("state = %+v; persistence = %+v", state, l.PersistStatus())
			}
			clock.fire()
			if clock.timers() != 0 || clock.executions.Load() != 0 {
				t.Fatal("unsafe countdown or execution")
			}
		})
	}
}

func TestHostPowerExternalFinalization(t *testing.T) {
	for _, kind := range []string{"delayed_success", "early_success", "external_error", "cancel_after_run"} {
		t.Run(kind, func(t *testing.T) {
			controller, clock := newHostPowerTestController(t)
			svc, id := hostPowerTestGoals(t)
			ctx, _ := hostPowerTestApproval(t)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			p := &hostPowerTestProvider{}
			l := makeLoop(t, p, tools.NewRegistry(), "system")
			l.ConfigureHostPower(controller, svc, true)
			p.hook = func(ctx context.Context) error {
				if err := armHostPowerTest(ctx, svc, id); err != nil {
					return err
				}
				completeHostPowerTestGoal(t, svc, id)
				if kind == "early_success" {
					l.FinalizeHostPower(nil)
					if clock.timers() != 0 {
						t.Error("early finalizer bypassed run cleanup")
					}
				}
				return nil
			}
			runHostPowerTest(t, l, ctx)
			if kind != "early_success" {
				if controller.Snapshot().Status != "armed" || clock.timers() != 0 {
					t.Fatal("run-end finalized external invocation")
				}
				if _, err := l.Run(ctx, "next run"); err == nil {
					t.Fatal("run reuse allowed before external finalization")
				}
			}
			var finalErr error
			if kind == "external_error" {
				finalErr = errors.New("frontend persistence failed")
			}
			if kind == "cancel_after_run" {
				cancel()
			}
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); l.FinalizeHostPower(finalErr) }()
			}
			wg.Wait()
			want := "countdown"
			if finalErr != nil || kind == "cancel_after_run" {
				want = "blocked"
			}
			if state := controller.Snapshot(); state.Status != want {
				t.Fatalf("state = %+v, want %s", state, want)
			}
			clock.fire()
			if want == "countdown" && (clock.timers() != 1 || clock.executions.Load() != 1) {
				t.Fatal("successful external completion not exactly once")
			}
			if want == "blocked" && (clock.timers() != 0 || clock.executions.Load() != 0) {
				t.Fatal("failed external cleanup executed")
			}
		})
	}
}
