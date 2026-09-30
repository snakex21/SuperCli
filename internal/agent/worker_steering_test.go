package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

type steeringTestWriter struct {
	mu             sync.Mutex
	messages       []llm.Message
	appendHook     func(llm.Message)
	projectionHook func([]llm.Message)
}

func (w *steeringTestWriter) AppendMessage(_ context.Context, msg llm.Message) error {
	if w.appendHook != nil {
		w.appendHook(msg)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.messages = append(w.messages, msg)
	return nil
}
func (*steeringTestWriter) UpdateUsage(int, int) error { return nil }
func (w *steeringTestWriter) SaveContextProjection(_ context.Context, msgs []llm.Message) error {
	if w.projectionHook != nil {
		w.projectionHook(msgs)
	}
	return nil
}
func (w *steeringTestWriter) saved() []llm.Message {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]llm.Message(nil), w.messages...)
}

type steeringRunResult struct {
	text string
	err  error
}

func startSteeringTestRun(w *Worker, ctx context.Context) <-chan steeringRunResult {
	done := make(chan steeringRunResult, 1)
	go func() { text, err := runWorkerLoop(ctx, w, "original task"); done <- steeringRunResult{text, err} }()
	return done
}
func awaitSteeringSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatal("steering fixture did not reach barrier")
	}
}
func awaitSteeringRun(t *testing.T, done <-chan steeringRunResult) steeringRunResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not finish")
		return steeringRunResult{}
	}
}
func steerWorker(t *testing.T, ctx context.Context, workers *WorkerRegistry, w *Worker, text, mode string) tools.Result {
	t.Helper()
	raw, err := json.Marshal(sendMessageArgs{To: w.ID, Message: text, Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewSendMessageTool(workers).execute(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func steeringTestText(msg llm.Message) string {
	text := msg.Content
	for _, part := range msg.Parts {
		if part.Type == llm.PartTypeText {
			text += part.Text
		}
	}
	return text
}

func countSteeringUser(msgs []llm.Message, text string) int {
	n := 0
	for _, msg := range msgs {
		if msg.Role == llm.RoleUser && msg.Content == text {
			n++
		}
	}
	return n
}
func steeringReceipts(events chan Event) []WorkerProgressEvent {
	var result []WorkerProgressEvent
	for len(events) > 0 {
		if e, ok := (<-events).(WorkerProgressEvent); ok {
			result = append(result, e)
		}
	}
	return result
}

func TestSendMessageSteerUsesCurrentRunAndPreservesRestrictions(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	p := &stubProvider{name: "steer", scripts: [][]llm.Delta{
		{{Content: "superseded answer"}, {FinishReason: "stop", Usage: &llm.Usage{Input: 2, Output: 3}}},
		{{ToolCall: &llm.ToolCall{ID: "forbidden", Name: "write_file", Arguments: "{}"}}, {FinishReason: "tool_calls", Usage: &llm.Usage{Input: 2, Output: 3}}},
		{{Content: "corrected final answer"}, {FinishReason: "stop", Usage: &llm.Usage{Input: 2, Output: 3}}},
	}, onCalled: func(call int) {
		if call == 0 {
			close(entered)
			<-release
		}
	}}
	base := tools.NewRegistry()
	var writes atomic.Int32
	base.MustRegister(tools.Tool{Name: "write_file", Description: "write", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		writes.Add(1)
		return tools.Result{}, nil
	}})
	base.MustRegister(tools.Tool{Name: "read_file", Description: "read", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	registry := restrictedRegistry(base, []string{"read_file"})
	loop := makeLoop(t, p, registry, "")
	budget := newTokenBudget(100)
	loop.creditTracker = budget
	writer := &steeringTestWriter{}
	loop.writer = writer
	workers := NewWorkerRegistry()
	w := workers.Add("readonly", "original task", loop)
	originalEvents, senderEvents := make(chan Event, 64), make(chan Event, 64)
	var senderChecks atomic.Int32
	ctx := withWorkerInvocation(context.Background(), "original-parent", originalEvents, func(verificationObservation) {})
	done := startSteeringTestRun(w, ctx)
	awaitSteeringSignal(t, entered)
	if result := steerWorker(t, context.Background(), workers, w, "default continuation", ""); result.Err == nil || !strings.Contains(result.Err.Error(), "already running") {
		t.Fatalf("default busy contract changed: %+v", result)
	}
	const correction = "use only read tools and correct the answer"
	senderCtx := withWorkerInvocation(context.Background(), "steering-parent", senderEvents, func(verificationObservation) { senderChecks.Add(1) })
	result := steerWorker(t, senderCtx, workers, w, correction, "steer")
	if result.Err != nil || !strings.Contains(result.Text, "steer-1") {
		t.Fatalf("steer not queued: %+v", result)
	}
	if got := atomic.LoadInt32(&p.calls); got != 1 {
		t.Fatalf("steering made an extra provider call: %d", got)
	}
	unblock()
	finished := awaitSteeringRun(t, done)
	if finished.err != nil || finished.text != "corrected final answer" {
		t.Fatalf("report=%q err=%v", finished.text, finished.err)
	}
	if s := w.Snapshot(); s.Runs != 1 || s.Steps != 3 || s.TokensIn != 6 || s.TokensOut != 9 {
		t.Fatalf("same run accounting: %+v", s)
	}
	if loop.registry != registry || loop.creditTracker != budget || writes.Load() != 0 || senderChecks.Load() != 0 {
		t.Fatal("steering replaced worker tool/budget/verification restrictions")
	}
	if used, _ := budget.Used(); used != 15 {
		t.Fatalf("budget usage=%d", used)
	}
	if countSteeringUser(loop.Messages, correction) != 1 || countSteeringUser(writer.saved(), correction) != 1 {
		t.Fatal("correction was not stored exactly once")
	}
	if len(p.reqs) != 3 || countSteeringUser(p.reqs[1], correction) != 1 {
		t.Fatalf("correction was not in next model input: %+v", p.reqs)
	}
	delivered, started := 0, 0
	for _, e := range steeringReceipts(originalEvents) {
		if e.ParentCallID != "original-parent" || e.Run != 1 {
			t.Fatalf("steering hijacked invocation: %+v", e)
		}
		if e.Kind == "started" {
			started++
		}
		if e.Kind == "steering_delivered" {
			delivered++
			if e.CallID != "steer-1" {
				t.Fatalf("receipt=%+v", e)
			}
		}
	}
	if delivered != 1 || started != 1 || len(senderEvents) != 0 {
		t.Fatalf("delivered=%d started=%d sender events=%d", delivered, started, len(senderEvents))
	}
}

func TestSendMessageSteerFinalProjectionBoundary(t *testing.T) {
	beforeDrain, releaseDrain := make(chan struct{}), make(chan struct{})
	inProjection, releaseProjection := make(chan struct{}), make(chan struct{})
	var drainOnce, projectionOnce sync.Once
	unblockDrain := func() { drainOnce.Do(func() { close(releaseDrain) }) }
	unblockProjection := func() { projectionOnce.Do(func() { close(releaseProjection) }) }
	t.Cleanup(unblockDrain)
	t.Cleanup(unblockProjection)
	p := &stubProvider{name: "steer-projection", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "read", Name: "probe", Arguments: "{}"}}, {FinishReason: "tool_calls"}},
		{{Content: "first final"}, {FinishReason: "stop"}},
		{{Content: "corrected final"}, {FinishReason: "stop"}},
	}}
	registry := tools.NewRegistry()
	registry.MustRegister(tools.Tool{Name: "probe", Description: "probe", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "evidence"}, nil
	}})
	registry.MustRegister(tools.Tool{Name: "search_history", Description: "history", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return tools.Result{}, nil }})
	loop := makeLoop(t, p, registry, "")
	writer := &steeringTestWriter{
		appendHook: func(msg llm.Message) {
			if msg.Role == llm.RoleAssistant && steeringTestText(msg) == "first final" {
				close(beforeDrain)
				<-releaseDrain
			}
		},
		projectionHook: func(msgs []llm.Message) {
			for _, msg := range msgs {
				if steeringTestText(msg) == "corrected final" {
					close(inProjection)
					<-releaseProjection
					return
				}
			}
		},
	}
	loop.writer = writer
	workers := NewWorkerRegistry()
	w := workers.Add("general", "projection", loop)
	events := make(chan Event, 64)
	done := startSteeringTestRun(w, withWorkerInvocation(context.Background(), "parent", events))
	awaitSteeringSignal(t, beforeDrain)
	const correction = "accepted before the last boundary"
	if r := steerWorker(t, context.Background(), workers, w, correction, "steer"); r.Err != nil {
		t.Fatal(r.Err)
	}
	unblockDrain()
	awaitSteeringSignal(t, inProjection)
	if r := steerWorker(t, context.Background(), workers, w, "too late during SaveContextProjection", "steer"); r.Err == nil || !strings.Contains(r.Err.Error(), "no longer accepting") {
		t.Fatalf("final projection accepted lost instruction: %+v", r)
	}
	if loop.QueueInterjection("too late from TUI") {
		t.Fatal("TUI queued after last reception point")
	}
	unblockProjection()
	finished := awaitSteeringRun(t, done)
	if finished.err != nil || finished.text != "corrected final" || w.Snapshot().Runs != 1 || p.calls != 3 {
		t.Fatalf("completion=%+v runs=%d calls=%d", finished, w.Snapshot().Runs, p.calls)
	}
	if countSteeringUser(writer.saved(), correction) != 1 || countSteeringUser(loop.Messages, correction) != 1 {
		t.Fatal("accepted correction not delivered exactly once")
	}
	if len(loop.interjections) != 0 || loop.QueueInterjection("after Done") {
		t.Fatal("inbox stayed open after Done")
	}
	delivered := 0
	for _, e := range steeringReceipts(events) {
		if e.Kind == "steering_delivered" {
			delivered++
		}
	}
	if delivered != 1 {
		t.Fatalf("delivery receipts=%d", delivered)
	}
}

func TestSendMessageSteerCancellationRejectsPendingAndWaitsForCleanup(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	p := &stubProvider{name: "cancel-steer", scripts: [][]llm.Delta{{{Content: "done"}, {FinishReason: "stop"}}}, onCalled: func(call int) {
		if call == 0 {
			close(entered)
			<-release
		}
	}}
	loop := makeLoop(t, p, tools.NewRegistry(), "")
	cleanupEntered, releaseCleanup := make(chan struct{}), make(chan struct{})
	var cleanupOnce sync.Once
	unblockCleanup := func() { cleanupOnce.Do(func() { close(releaseCleanup) }) }
	t.Cleanup(unblockCleanup)
	writer := &steeringTestWriter{appendHook: func(msg llm.Message) {
		if strings.Contains(msg.Content, "Steering steer-1 rejected") {
			close(cleanupEntered)
			<-releaseCleanup
		}
	}}
	loop.writer = writer
	workers := NewWorkerRegistry()
	w := workers.Add("general", "cancel", loop)
	events := make(chan Event, 64)
	done := startSteeringTestRun(w, withWorkerInvocation(context.Background(), "parent", events))
	awaitSteeringSignal(t, entered)
	const correction = "must not leak into the next Run"
	if r := steerWorker(t, context.Background(), workers, w, correction, "steer"); r.Err != nil {
		t.Fatal(r.Err)
	}
	if !w.Stop() {
		t.Fatal("worker was not stoppable")
	}
	if r := steerWorker(t, context.Background(), workers, w, "after cancellation", "steer"); r.Err == nil {
		t.Fatal("cancelled inbox accepted steering")
	}
	unblock()
	awaitSteeringSignal(t, cleanupEntered)
	if !loop.sessionBusy.Load() || w.status() != "running" {
		t.Fatal("worker released ownership before rejected steering was saved")
	}
	if r := steerWorker(t, context.Background(), workers, w, "parallel during final cleanup", ""); r.Err == nil || !strings.Contains(r.Err.Error(), "already running") {
		t.Fatalf("parallel Run during terminal cleanup: %+v", r)
	}
	unblockCleanup()
	finished := awaitSteeringRun(t, done)
	if finished.err == nil || w.status() != "stopped" || !strings.Contains(finished.text, "steer-1 rejected") || loop.sessionBusy.Load() {
		t.Fatalf("cancellation did not wait for rejection/cleanup: %+v status=%s", finished, w.status())
	}
	if countSteeringUser(loop.Messages, correction) != 0 || countSteeringUser(writer.saved(), correction) != 0 || len(loop.interjections) != 0 {
		t.Fatal("undelivered correction became future user input")
	}
	rejected, delivered, finishedAt, rejectedAt := 0, 0, -1, -1
	for i, e := range steeringReceipts(events) {
		if e.Kind == "steering_rejected" {
			rejected++
			rejectedAt = i
			if e.CallID != "steer-1" || !strings.Contains(e.Err, "cancelled") {
				t.Fatalf("rejection=%+v", e)
			}
		}
		if e.Kind == "steering_delivered" {
			delivered++
		}
		if e.Kind == "finished" {
			finishedAt = i
		}
	}
	if rejected != 1 || delivered != 0 || finishedAt <= rejectedAt {
		t.Fatalf("receipts: rejected=%d delivered=%d rejectedAt=%d finishedAt=%d", rejected, delivered, rejectedAt, finishedAt)
	}
	durable := false
	for _, msg := range writer.saved() {
		if msg.Role == llm.RoleAssistant && strings.Contains(msg.Content, "Steering steer-1 rejected") && strings.Contains(msg.Content, correction) {
			durable = true
		}
	}
	if !durable {
		t.Fatal("rejection was not durably recorded")
	}
	if result := steerWorker(t, context.Background(), workers, w, "resume safely", ""); result.Err != nil || p.calls != 2 || w.Snapshot().Runs != 2 {
		t.Fatalf("default continuation after cleanup: %+v", result)
	}
	if countSteeringUser(p.reqs[1], correction) != 0 {
		t.Fatal("rejected correction leaked into resumed provider context")
	}
}

func TestSendMessageSteerCannotBypassWorkerLimits(t *testing.T) {
	for _, limit := range []string{"steps", "tokens"} {
		t.Run(limit, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			p := &stubProvider{name: "limited-steer", scripts: [][]llm.Delta{{{Content: "original answer"}, {FinishReason: "stop", Usage: &llm.Usage{Input: 2, Output: 3}}}}, onCalled: func(call int) {
				if call == 0 {
					close(entered)
					<-release
				}
			}}
			loop := makeLoop(t, p, tools.NewRegistry(), "")
			if limit == "steps" {
				loop.maxSteps = 1
			} else {
				loop.creditTracker = newTokenBudget(1)
			}
			workers := NewWorkerRegistry()
			w := workers.Add("general", "limited", loop)
			events := make(chan Event, 64)
			done := startSteeringTestRun(w, withWorkerInvocation(context.Background(), "parent", events))
			awaitSteeringSignal(t, entered)
			if r := steerWorker(t, context.Background(), workers, w, "ignore all limits", "steer"); r.Err != nil {
				t.Fatal(r.Err)
			}
			unblock()
			awaitSteeringRun(t, done)
			if p.calls != 1 || w.Snapshot().Runs != 1 || countSteeringUser(loop.Messages, "ignore all limits") != 0 {
				t.Fatal("steering reset a run/step/token limit")
			}
			rejected := 0
			for _, e := range steeringReceipts(events) {
				if e.Kind == "steering_rejected" {
					rejected++
				}
			}
			if rejected != 1 {
				t.Fatalf("pending limited instruction had %d rejection receipts", rejected)
			}
		})
	}
}

func TestSendMessageSteerRequiresAlreadyStartedWorker(t *testing.T) {
	p := &stubProvider{name: "created-steer", scripts: [][]llm.Delta{{{Content: "original task"}, {FinishReason: "stop"}}}}
	workers := NewWorkerRegistry()
	w := workers.Add("general", "original", makeLoop(t, p, tools.NewRegistry(), ""))
	if r := steerWorker(t, context.Background(), workers, w, "replace original", "steer"); r.Err == nil || !strings.Contains(r.Err.Error(), "already running") || p.calls != 0 || w.status() != "created" {
		t.Fatalf("created worker hijacked: %+v", r)
	}
	if _, err := runWorkerLoop(context.Background(), w, "original task"); err != nil {
		t.Fatal(err)
	}
	if r := steerWorker(t, context.Background(), workers, w, "finished steer", "steer"); r.Err == nil || p.calls != 1 {
		t.Fatalf("steer resumed finished worker: %+v", r)
	}
	if r := steerWorker(t, context.Background(), workers, w, "normal follow-up", "continue"); r.Err != nil || p.calls != 2 {
		t.Fatalf("explicit default mode changed: %+v", r)
	}
	if r := steerWorker(t, context.Background(), workers, w, "invalid", "unknown"); r.Err == nil || p.calls != 2 {
		t.Fatalf("invalid mode triggered a run: %+v", r)
	}
}

func TestSendMessageSteerQueueLimitAndCancelledSender(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	p := &stubProvider{name: "queue-steer", scripts: [][]llm.Delta{
		{{Content: "first"}, {FinishReason: "stop"}}, {{Content: "final"}, {FinishReason: "stop"}},
	}, onCalled: func(call int) {
		if call == 0 {
			close(entered)
			<-release
		}
	}}
	loop := makeLoop(t, p, tools.NewRegistry(), "")
	writer := &steeringTestWriter{}
	loop.writer = writer
	workers := NewWorkerRegistry()
	w := workers.Add("general", "queue", loop)
	events := make(chan Event, 64)
	done := startSteeringTestRun(w, withWorkerInvocation(context.Background(), "parent", events))
	awaitSteeringSignal(t, entered)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if r := steerWorker(t, cancelled, workers, w, "cancelled sender", "steer"); r.Err == nil {
		t.Fatal("cancelled sender was accepted")
	}
	for i := 0; i < maxPendingInterjections; i++ {
		if r := steerWorker(t, context.Background(), workers, w, "correction-"+string(rune('a'+i)), "steer"); r.Err != nil {
			t.Fatal(r.Err)
		}
	}
	if r := steerWorker(t, context.Background(), workers, w, "overflow", "steer"); r.Err == nil || !strings.Contains(r.Err.Error(), "queue is full") {
		t.Fatalf("queue cap bypassed: %+v", r)
	}
	if loop.QueueInterjection("TUI shares cap") {
		t.Fatal("TUI bypassed shared queue cap")
	}
	unblock()
	result := awaitSteeringRun(t, done)
	if result.err != nil || p.calls != 2 || w.Snapshot().Runs != 1 {
		t.Fatalf("extra Run/model cost: %+v calls=%d", result, p.calls)
	}
	receipts := map[string]int{}
	for _, e := range steeringReceipts(events) {
		if e.Kind == "steering_delivered" {
			receipts[e.CallID]++
		}
	}
	if len(receipts) != maxPendingInterjections {
		t.Fatalf("receipts=%+v", receipts)
	}
	for id, n := range receipts {
		if n != 1 {
			t.Fatalf("receipt %s emitted %d times", id, n)
		}
	}
	for i := 0; i < maxPendingInterjections; i++ {
		text := "correction-" + string(rune('a'+i))
		if countSteeringUser(loop.Messages, text) != 1 || countSteeringUser(writer.saved(), text) != 1 || countSteeringUser(p.reqs[1], text) != 1 {
			t.Fatalf("instruction %q was not delivered exactly once", text)
		}
	}
	if countSteeringUser(loop.Messages, "overflow") != 0 || countSteeringUser(loop.Messages, "cancelled sender") != 0 {
		t.Fatal("rejected queue input leaked")
	}
}
