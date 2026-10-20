package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/goal"
	"supercli/internal/tools"
	toolhostpower "supercli/internal/tools/hostpower"
)

func TestHostPowerFollowupWhileWorkBlocked(t *testing.T) {
	for _, kind := range []string{"human_arbitrary", "human_other_language", "human_rearm", "private_worker", "background_user_role", "unconfigured_child", "blank"} {
		t.Run(kind, func(t *testing.T) {
			controller, clock := newHostPowerTestController(t)
			svc, id := hostPowerTestGoals(t)
			ctx, approvals := hostPowerTestApproval(t)
			entered := make(chan context.Context, 1)
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			registry := tools.NewRegistry()
			registry.MustRegister(tools.Tool{Name: "fixture_work", Description: "Block inert fixture work before goal completion", Schema: `{}`, Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
				entered <- ctx
				select {
				case <-release:
				case <-ctx.Done():
					return tools.Result{}, ctx.Err()
				}
				completeHostPowerTestGoal(t, svc, id)
				return tools.Result{Text: "Original work completed, without interruption."}, nil
			}})
			registry.MustRegister(toolhostpower.Spec())
			loop := makeLoop(t, &stubProvider{name: "followup-fixture", scripts: hostPowerIntegrationScripts(id)}, registry, "system")
			loop.ConfigureHostPower(controller, svc, false)
			out, err := loop.Run(ctx, "Complete fixture work and then power off")
			if err != nil {
				t.Fatal(err)
			}
			workCtx := <-entered
			if controller.Snapshot().Status != "armed" {
				t.Fatal("fixture did not arm before blocking work")
			}
			message := "Also retain a concise summary."
			if kind == "human_other_language" {
				message = "Nie wyłączaj komputera; kontynuuj zadanie."
			}
			human := strings.HasPrefix(kind, "human_")
			switch kind {
			case "human_arbitrary", "human_other_language", "human_rearm":
				if !loop.QueueInterjection(message) {
					t.Fatal("human input was rejected")
				}
			case "private_worker":
				if _, err := loop.queueInterjection("worker steering", true); err != nil {
					t.Fatal(err)
				}
			case "background_user_role":
				loop.InjectUserMessage(workCtx, "worker report with a user role")
			case "unconfigured_child":
				child := makeLoop(t, &hostPowerTestProvider{}, tools.NewRegistry(), "child")
				if child.QueueInterjection("child cannot revoke root consent") {
					t.Fatal("idle child accepted steering")
				}
			case "blank":
				if loop.QueueInterjection("  \n ") {
					t.Fatal("blank input accepted")
				}
			}
			want := "armed"
			if human {
				want = "cancelled"
			}
			if state := controller.Snapshot(); state.Status != want {
				t.Fatalf("while work blocked: %+v, want %s", state, want)
			}
			if workCtx.Err() != nil {
				t.Fatal("followup cancelled the working command/run context")
			}
			g, err := svc.Goal(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if g.Status != goal.StatusActive {
				t.Fatal("withdrawal pretended unfinished goal was complete")
			}
			if kind == "human_rearm" {
				if err := armHostPowerToolTest(workCtx, id); err != nil {
					t.Fatal(err)
				}
				if approvals.Load() != 2 || controller.Snapshot().Status != "armed" {
					t.Fatal("rearm did not require fresh trusted UI consent")
				}
			}
			once.Do(func() { close(release) })
			events := drainEvents(t, out)
			done := false
			for _, event := range events {
				if event, ok := event.(ErrorEvent); ok {
					t.Fatalf("original run failed: %v", event.Err)
				}
				if _, ok := event.(DoneEvent); ok {
					done = true
				}
			}
			if !done {
				t.Fatal("original work did not finish")
			}
			if human {
				found := false
				for _, msg := range loop.Messages {
					if msg.Role == llm.RoleUser && msg.Content == message {
						found = true
					}
				}
				if !found {
					t.Fatal("main model did not retain the human's actual wording")
				}
			}
			cancelled := human && kind != "human_rearm"
			want = "countdown"
			if cancelled {
				want = "cancelled"
			}
			if state := controller.Snapshot(); state.Status != want {
				t.Fatalf("after original work: %+v, want %s", state, want)
			}
			clock.fire()
			clock.fire()
			wantExec := int32(1)
			if cancelled {
				wantExec = 0
			}
			if clock.executions.Load() != wantExec {
				t.Fatalf("fake executions = %d, want %d", clock.executions.Load(), wantExec)
			}
		})
	}
}

func TestHostPowerFollowupRacesRunCompletion(t *testing.T) {
	controller, clock := newHostPowerTestController(t)
	svc, id := hostPowerTestGoals(t)
	ctx, _ := hostPowerTestApproval(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var workOnce sync.Once
	provider := &hostPowerTestProvider{hook: func(ctx context.Context) error {
		var armErr error
		workOnce.Do(func() {
			armErr = armHostPowerToolTest(ctx, id)
			if armErr != nil {
				return
			}
			completeHostPowerTestGoal(t, svc, id)
			close(entered)
			<-release
		})
		return armErr
	}}
	loop := makeLoop(t, provider, tools.NewRegistry(), "system")
	loop.ConfigureHostPower(controller, svc, true)
	out, err := loop.Run(ctx, "Finish fixture work then power off")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); loop.QueueInterjection("new trusted scope") }()
	}
	once.Do(func() { close(release) })
	drainEvents(t, out)
	wg.Wait()
	loop.FinalizeHostPower(nil)
	if state := controller.Snapshot(); state.Status != "cancelled" {
		t.Fatalf("completion restored withdrawn consent: %+v", state)
	}
	clock.fire()
	if clock.executions.Load() != 0 {
		t.Fatal("withdrawn power consent executed")
	}
}
