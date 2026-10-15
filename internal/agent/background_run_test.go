package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestBackgroundRunWaitIsCancellableAndCoalescesCompletion(t *testing.T) {
	run := &backgroundRun{}
	finishA, finishB := run.start(), run.start()
	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan struct{})
	done := make(chan error, 1)
	go func() { _, err := run.wait(ctx, 0, func() { close(waiting) }); done <- err }()
	<-waiting
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	finishA()
	finishA()
	run.publishReport()
	run.publishReport()
	finishB()
	resume, err := run.wait(context.Background(), 0, nil)
	if err != nil || !resume || run.revision() != 0 {
		t.Fatal("completion count or resume lost")
	}
	run.acceptReport()
	run.acceptReport()
	resume, err = run.wait(context.Background(), 2, nil)
	if err != nil || resume {
		t.Fatal("already consumed reports generated an extra turn")
	}
}

func TestBackgroundReportAlreadyUsedDoesNotAddCoordinatorTurn(t *testing.T) {
	reg := tools.NewRegistry()
	provider := makeScriptedProvider("Report applied.")
	provider.scripts = [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "async-fixture", Name: "fixture_async", Arguments: "{}"}, FinishReason: "tool_calls"}},
		{{Content: "Report applied.", FinishReason: "stop"}},
	}
	parent, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	var finish func()
	reg.MustRegister(tools.Tool{Name: "fixture_async", Description: "fixture", Schema: "{}", Fn: func(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
		finish = backgroundRunFrom(ctx).start()
		// Delivery precedes worker teardown, as in startBackgroundWorker.
		parent.backgroundDelivery(ctx)(ctx, "REVIEW_ALREADY_IN_REQUEST")
		return tools.Result{Text: "worker started"}, nil
	}})
	reg.MarkAlwaysOn("fixture_async")
	provider.onCalled = func(index int) {
		if index == 1 {
			finish()
		}
	}
	events, err := parent.Run(context.Background(), "Use the delegated report.")
	if err != nil {
		t.Fatal(err)
	}
	done := false
	for event := range events {
		switch event := event.(type) {
		case DoneEvent:
			done = true
		case ErrorEvent:
			t.Error(event.Err)
		}
	}
	if !done || provider.calls != 2 {
		t.Fatalf("an already consumed report added a model round: done=%v calls=%d", done, provider.calls)
	}
	found := false
	for _, message := range provider.reqs[1] {
		found = found || strings.Contains(message.Content, "REVIEW_ALREADY_IN_REQUEST")
	}
	if !found {
		t.Fatal("the report was not in the coordinator request")
	}
}

func TestBackgroundCompletionCannotWakeAnotherRun(t *testing.T) {
	prior, current := &backgroundRun{}, &backgroundRun{}
	finish := prior.start()
	prior.publishReport()
	prior.acceptReport()
	finish()
	resume, err := current.wait(context.Background(), current.revision(), nil)
	if err != nil || resume {
		t.Fatalf("another run's completion triggered continuation: %v %v", resume, err)
	}
}

func TestAsyncTaskResultsResumeCoordinatorWithoutUserContinue(t *testing.T) {
	reg := tools.NewRegistry()
	coordinator := makeScriptedProvider("All reviews applied.")
	coordinator.scripts = [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "review-a", Name: "task", Arguments: `{"prompt":"review the isolated fixture","async":true}`}, FinishReason: "tool_calls"}},
		{{Content: "Waiting for the review.", FinishReason: "stop"}},
		{{Content: "All reviews applied.", FinishReason: "stop"}},
	}
	parent, err := NewLoop(LoopConfig{Provider: coordinator, Registry: reg, MaxSteps: 6})
	if err != nil {
		t.Fatal(err)
	}
	child := makeScriptedProvider("REVIEW_READY: use the checked result")
	release := make(chan struct{})
	child.onCalled = func(int) { <-release }
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	task, err := NewAgentTool(specs, parent, reg, child, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	reg.MustRegister(task.Spec())
	reg.MarkAlwaysOn("task")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := parent.Run(ctx, "Delegate a review, use its findings and finish the work.")
	if err != nil {
		t.Fatal(err)
	}
	released, done := false, false
	waiting, waitIntervals := false, 0
	for event := range events {
		switch event := event.(type) {
		case DelegatedWaitEvent:
			if event.Waiting == waiting {
				t.Error("unpaired delegated wait event")
			}
			waiting = event.Waiting
			if waiting {
				waitIntervals++
			}
		case NoticeEvent:
			if strings.Contains(event.Text, "Waiting for delegated") && !released {
				close(release)
				released = true
			}
		case DoneEvent:
			done = true
		case ErrorEvent:
			t.Error(event.Err)
		}
	}
	if !released {
		close(release)
	}
	if !done || coordinator.calls != 3 {
		t.Fatalf("coordinator stopped early: done=%v calls=%d", done, coordinator.calls)
	}
	if waiting || waitIntervals != 1 {
		t.Fatalf("delegated wait did not close before completion: waiting=%v intervals=%d", waiting, waitIntervals)
	}
	found := false
	for _, message := range coordinator.reqs[len(coordinator.reqs)-1] {
		if strings.Contains(message.Content, "REVIEW_READY") {
			found = true
		}
	}
	if !found {
		t.Fatal("resumed coordinator did not receive the worker report")
	}
}
