package webgui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/checkpoint"
	"supercli/internal/llm"
)

type deferredWireCompletion struct {
	record *checkpoint.Record
	err    error
}
type deferredWireProvider struct {
	eng                      *Engine
	mu                       sync.Mutex
	parentCalls, workerCalls int
	started                  chan struct{}
	done                     chan deferredWireCompletion
	borrow                   *checkpoint.InvocationBorrow
}

func (*deferredWireProvider) Name() string { return "synthetic-deferred-wire" }
func (p *deferredWireProvider) Complete(ctx context.Context, messages []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	user := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleUser {
			user = messages[i].Content
			break
		}
	}
	if strings.HasPrefix(user, "deferred-wire-worker") {
		p.mu.Lock()
		p.workerCalls++
		n := p.workerCalls
		p.mu.Unlock()
		if n == 1 {
			return workerSteeringWireCall("deferred-write", "write_file", map[string]any{"path": "a.txt", "content": "after"})
		}
		if n != 2 {
			return nil, fmt.Errorf("worker repeated a completed task")
		}
		manager, err := p.eng.checkpointManager(p.eng.Home())
		if err != nil {
			return nil, err
		}
		borrow, err := checkpoint.BorrowInvocation(ctx, true)
		if err != nil {
			return nil, err
		}
		turn, leave, err := checkpoint.EnterBoundMutation(ctx, manager, nil, nil)
		if err != nil {
			borrow.Close()
			return nil, err
		}
		leave()
		// Public completion API lets the fixture observe the real finalizer.
		// It deliberately leaves SQL unpublished, like a crashed/transient sink,
		// while the foreground must still insert its binding on the first write.
		if _, err := turn.CompleteDeferred(ctx, func(record *checkpoint.Record, err error) { p.done <- deferredWireCompletion{record, err} }); err != nil {
			borrow.Close()
			return nil, err
		}
		p.mu.Lock()
		p.borrow = borrow
		p.mu.Unlock()
		close(p.started)
		return workerSteeringWireDeltas(llm.Delta{Content: "worker done", FinishReason: "stop"}), nil
	}
	p.mu.Lock()
	p.parentCalls++
	n := p.parentCalls
	p.mu.Unlock()
	if n == 1 {
		return workerSteeringWireCall("deferred-task", "task", map[string]any{"agent": "code", "prompt": "deferred-wire-worker", "async": true})
	}
	select {
	case <-p.started:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return workerSteeringWireDeltas(llm.Delta{Content: "worker accepted", FinishReason: "stop"}), nil
}

func TestWebStreamPersistsDeferredBindingOnFirstSummaryInsert(t *testing.T) {
	eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	provider := &deferredWireProvider{eng: eng, started: make(chan struct{}), done: make(chan deferredWireCompletion, 1)}
	eng.prov = provider
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cleanup := func() {
		provider.mu.Lock()
		borrow := provider.borrow
		provider.mu.Unlock()
		if borrow != nil {
			borrow.Close()
		}
	}
	defer cleanup()
	sid := ""
	if err := eng.runStream(ctx, "perform the synthetic deferred file edit", "", "", func(ev wireEvent) {
		if ev.Type == "session" {
			sid = ev.SessionID
		}
	}); err != nil {
		t.Fatal(err)
	}
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ReadTurnSummaries(ctx, sid)
	if err != nil || len(rows) != 1 || rows[0].CheckpointBinding == nil || rows[0].CheckpointBinding.Resolved {
		t.Fatalf("first summary lost deferred owner: %+v %v", rows, err)
	}
	receipt, err := store.ReadUserReceiptAt(ctx, sid, 1)
	if err != nil || rows[0].CheckpointBinding.UserMessageID != receipt.ID {
		t.Fatal("summary not bound to original user receipt")
	}
	key := rows[0].CheckpointBinding.Key
	cleanup()
	select {
	case completed := <-provider.done:
		if completed.err != nil || completed.record == nil || completed.record.CompletionKey != key {
			t.Fatalf("durable record lost prepared key: %+v", completed)
		}
	case <-ctx.Done():
		t.Fatal("event-driven finalizer did not complete")
	}
	messages, err := eng.transcript(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msg := range messages {
		if msg.Turn != nil && len(msg.Turn.FileChanges) == 1 && msg.Turn.FileChanges[0].Path == "a.txt" {
			found = true
		}
	}
	if !found {
		t.Fatal("actual transcript path did not recover worker changes")
	}
	provider.mu.Lock()
	parentCalls, workerCalls := provider.parentCalls, provider.workerCalls
	provider.mu.Unlock()
	if parentCalls != 2 || workerCalls != 2 {
		t.Fatalf("repair added model calls: parent=%d worker=%d", parentCalls, workerCalls)
	}
}
