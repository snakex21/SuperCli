package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func interruptedOutputFixture(t *testing.T, persistence tools.OutputPersistence, result tools.Result) (*Loop, toolResult, ToolResultEvent) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry := tools.NewRegistry()
	registry.MustRegister(tools.Tool{
		Name: "interrupted_fixture", Description: "Fixture diagnostics", Schema: "{}", ReadOnly: true,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) { cancel(); return result, nil },
	})
	registry.MarkAlwaysOn("interrupted_fixture")
	loop, err := NewLoop(LoopConfig{Provider: echoProvider("fixture"), Registry: registry, ToolOutputs: persistence})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 4)
	outcome := loop.invoke(ctx, llm.ToolCall{ID: "interrupted", Name: "interrupted_fixture", Arguments: "{}"}, events)
	close(events)
	var resultEvent ToolResultEvent
	for event := range events {
		if result, ok := event.(ToolResultEvent); ok {
			resultEvent = result
		}
	}
	if !outcome.failed || len(outcome.followUps) != 1 || outcome.followUps[0].ToolCallID != "interrupted" || !strings.Contains(outcome.followUps[0].Content, "TOOL_OUTCOME_UNKNOWN") {
		t.Fatal("interrupted outcome or protocol changed")
	}
	if resultEvent.ID != "interrupted" || resultEvent.Output != result.Text || !errors.Is(resultEvent.Err, context.Canceled) {
		t.Fatal("interrupted UI result changed")
	}
	if loop.identicalFails.attempts("interrupted_fixture", "{}") != 0 || loop.concreteFailure.Load() || loop.failedChecks.unresolved() {
		t.Fatal("interruption became a model failure or verification outcome")
	}
	return loop, outcome, resultEvent
}

func TestInterruptedOutputSurvivesFreshLoop(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(fmt.Sprintf("retained=%v", retained), func(t *testing.T) {
			ctx := context.Background()
			store, err := session.OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			sess, err := store.Create("fixture-project", "fixture-model", "")
			if err != nil {
				t.Fatal(err)
			}
			writer := session.NewWriter(store, sess.ID)
			full := strings.Repeat("before diagnostic\n", 1700) + "distinct preserved evidence\n"
			full += strings.Repeat("d", 61933-len(full))
			result := tools.Result{Text: full, Err: context.Canceled}
			if retained {
				result.Text = "short displayed diagnostic"
				result.RetainedText = full
			}
			_, outcome, resultEvent := interruptedOutputFixture(t, writer, result)
			content := outcome.followUps[0].Content
			handle := handleInOutput(content)
			if handle == "" || resultEvent.OutputHandle != handle || !strings.Contains(content, "saved for later turns") {
				t.Fatal("interrupted diagnostics not advertised as saved")
			}
			if got, err := writer.ReadToolOutput(ctx, handle); err != nil || got != full {
				t.Fatalf("persisted diagnostics changed: %v", err)
			}
			fresh, err := NewLoop(LoopConfig{Provider: echoProvider("fixture"), Registry: tools.NewRegistry(), ToolOutputs: writer})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]any{"handle": handle, "query": "distinct preserved evidence"})
			read := fresh.invoke(ctx, llm.ToolCall{ID: "read-preserved", Name: "read_output", Arguments: string(raw)}, make(chan Event, 4))
			if read.failed || len(read.followUps) != 1 || !strings.Contains(read.followUps[0].Content, "distinct preserved evidence") {
				t.Fatal("fresh loop cannot retrieve interrupted evidence")
			}
			history, err := store.ReadMessages(ctx, sess.ID)
			if err != nil || len(history) != 0 {
				t.Fatal("output persistence wrote transcript messages")
			}
		})
	}
}

type interruptedOutputPersistence struct {
	fail          error
	awaitDeadline bool
	saves         int
	bounded       bool
}

func (s *interruptedOutputPersistence) SaveToolOutput(ctx context.Context, handle, text string) error {
	s.saves++
	deadline, ok := ctx.Deadline()
	s.bounded = ok && time.Until(deadline) > 0 && time.Until(deadline) <= time.Second
	if !s.bounded {
		return errors.New("missing bounded cleanup context")
	}
	if s.awaitDeadline {
		<-ctx.Done()
		return ctx.Err()
	}
	return s.fail
}
func (s *interruptedOutputPersistence) ReadToolOutput(context.Context, string) (string, error) {
	return "", errors.New("fixture has no persisted output")
}

func TestInterruptedOutputPersistenceFailureKeepsMemoryEvidence(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprintf("timeout=%v", timeout), func(t *testing.T) {
			persistence := &interruptedOutputPersistence{fail: errors.New("fixture persistence unavailable"), awaitDeadline: timeout}
			full := strings.Repeat("preserved evidence\n", 2000)
			loop, outcome, _ := interruptedOutputFixture(t, persistence, tools.Result{Text: "small displayed diagnostic", RetainedText: full, Err: context.Canceled})
			if persistence.saves != 1 || !persistence.bounded {
				t.Fatal("cleanup persistence was not bounded")
			}
			content := outcome.followUps[0].Content
			handle := handleInOutput(content)
			if handle == "" || !strings.Contains(content, "in memory only") || strings.Contains(content, "saved for later turns") {
				t.Fatal("failed save claimed durable output")
			}
			raw, _ := json.Marshal(map[string]any{"handle": handle, "query": "preserved evidence"})
			read := loop.invoke(context.Background(), llm.ToolCall{ID: "read-memory", Name: "read_output", Arguments: string(raw)}, make(chan Event, 4))
			if read.failed || len(read.followUps) != 1 || !strings.Contains(read.followUps[0].Content, "preserved evidence") {
				t.Fatal("persistence failure lost current-loop evidence")
			}
		})
	}
}

func TestCancelledSmallOutputDoesNotPersist(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(fmt.Sprintf("started=%v", started), func(t *testing.T) {
			persistence := &interruptedOutputPersistence{fail: errors.New("must not be called")}
			loop, err := NewLoop(LoopConfig{Provider: echoProvider("fixture"), Registry: tools.NewRegistry(), ToolOutputs: persistence})
			if err != nil {
				t.Fatal(err)
			}
			events := make(chan Event, 2)
			outcome := loop.cancelledToolResult(llm.ToolCall{ID: "pending", Name: "fixture", Arguments: "{}"}, tools.Result{Text: "small diagnostic"}, context.DeadlineExceeded, started, events)
			event := (<-events).(ToolResultEvent)
			marker := "TOOL_NOT_STARTED"
			if started {
				marker = "TOOL_OUTCOME_UNKNOWN"
			}
			if !outcome.failed || len(outcome.followUps) != 1 || !strings.Contains(outcome.followUps[0].Content, marker) || !errors.Is(event.Err, context.DeadlineExceeded) {
				t.Fatal("cancellation cause or outcome changed")
			}
			if persistence.saves != 0 || event.OutputHandle != "" {
				t.Fatal("small cancellation performed output persistence")
			}
		})
	}
}
