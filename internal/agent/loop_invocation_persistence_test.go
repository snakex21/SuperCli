package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

type invocationContextKey struct{}

type invocationContextProvider struct {
	*stubProvider
	contexts chan context.Context
}

func (p *invocationContextProvider) Complete(ctx context.Context, messages []llm.Message, definitions []llm.ToolDef) (<-chan llm.Delta, error) {
	p.contexts <- ctx
	return p.stubProvider.Complete(ctx, messages, definitions)
}

type invocationBlockedWriter struct {
	*session.Writer
	blocked *atomic.Bool
}

func (w *invocationBlockedWriter) AppendMessage(ctx context.Context, msg llm.Message) error {
	if w.blocked.Load() {
		return errors.New("synthetic store unavailable")
	}
	return w.Writer.AppendMessage(ctx, msg)
}

func invocationStore(t *testing.T) (*session.Store, session.Session) {
	t.Helper()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	chat, err := store.Create(t.TempDir(), "fixture", "invocation fixture")
	if err != nil {
		t.Fatal(err)
	}
	return store, chat
}

func invocationReceipt(writer SessionWriter) (int, int64) {
	if owner, ok := writer.(interface{ FirstUserReceipt() session.MessageReceipt }); ok {
		receipt := owner.FirstUserReceipt()
		return receipt.Seq, receipt.ID
	}
	return 0, 0
}

func invocationRun(t *testing.T, loop *Loop, ctx context.Context, prompt string) {
	t.Helper()
	events, err := loop.Run(ctx, prompt)
	if err != nil {
		t.Fatal(err)
	}
	for event := range events {
		if failed, ok := event.(ErrorEvent); ok {
			t.Errorf("synthetic run failed: %v", failed.Err)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}

func TestInvocationWriterTwoRunsBindBeforeModelWithoutResettingConversation(t *testing.T) {
	store, chat := invocationStore(t)
	initial := session.NewWriter(store, chat.ID)
	provider := &invocationContextProvider{stubProvider: makeScriptedProvider("answer"), contexts: make(chan context.Context, 2)}
	bound := []chan struct{}{make(chan struct{}), make(chan struct{})}
	ordering := make(chan bool, 2)
	provider.onCalled = func(call int) {
		select {
		case <-bound[call]:
			ordering <- true
		default:
			ordering <- false
		}
	}
	registry := emptyRegistry()
	registry.MustRegister(tools.Tool{Name: "fixture_discovered", Description: "synthetic discovery", Schema: "{}", ReadOnly: true, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Text: "fixture"}, nil
	}})
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, Writer: initial, System: "fixture policy"})
	if err != nil {
		t.Fatal(err)
	}
	loop.RestoreDiscoveredTools([]string{"fixture_discovered"})
	var writers []*session.Writer
	var receipts []session.MessageReceipt
	loop.SetInvocationPersistence(func(previous SessionWriter) SessionWriter {
		writer := session.NewWriter(store, previous.(interface{ SessionID() string }).SessionID())
		writers = append(writers, writer)
		return writer
	}, invocationReceipt, func(seq int, id int64) {
		receipts = append(receipts, session.MessageReceipt{Seq: seq, ID: id})
		close(bound[len(receipts)-1])
	})
	ctx := llm.WithOpenCodeSession(context.WithValue(context.Background(), invocationContextKey{}, "preserved"), chat.ID)
	for _, prompt := range []string{"first prompt", "second prompt"} {
		invocationRun(t, loop, ctx, prompt)
		if !<-ordering {
			t.Fatal("model started before the current prompt receipt was bound")
		}
		if received := <-provider.contexts; received.Value(invocationContextKey{}) != "preserved" {
			t.Fatal("invocation rotation replaced the caller's provider context")
		}
	}
	if len(writers) != 2 || writers[0] == writers[1] || writers[0] == initial || initial.FirstUserSeq() != 0 {
		t.Fatal("accepted runs did not obtain fresh invocation writers")
	}
	if len(receipts) != 2 || receipts[0].Seq != 1 || receipts[1].Seq != 3 || receipts[0].ID == receipts[1].ID {
		t.Fatalf("turns adopted the same prompt: %+v", receipts)
	}
	for i, receipt := range receipts {
		if current, err := store.IsCurrentUserReceipt(ctx, chat.ID, receipt); err != nil || !current || writers[i].FirstUserReceipt() != receipt {
			t.Fatalf("receipt did not identify this invocation: %+v, error=%v", receipt, err)
		}
	}
	rows, err := store.ReadMessages(ctx, chat.ID)
	if err != nil || len(rows) != 4 || rows[0].Content != "first prompt" || rows[2].Content != "second prompt" {
		t.Fatalf("rotation changed the durable conversation: count=%d, error=%v", len(rows), err)
	}
	if len(loop.Messages) != 5 || loop.Messages[0].Content != "fixture policy" || !registry.IsActive("fixture_discovered") {
		t.Fatal("writer rotation reset history, policy or discovered tools")
	}
	meta, err := store.Get(chat.ID)
	if err != nil || meta.TokenIn != 6 || meta.TokenOut != 10 {
		t.Fatalf("rotation reset durable cumulative usage: %+v, error=%v", meta, err)
	}
	if usage := loop.SessionUsage(); usage.Input != 6 || usage.Output != 10 {
		t.Fatalf("rotation reset Loop usage: %+v", usage)
	}
}

func TestInvocationBacklogKeepsOriginalWriterAndCannotStealNextPrompt(t *testing.T) {
	store, chat := invocationStore(t)
	loop, err := NewLoop(LoopConfig{Provider: makeScriptedProvider("answer"), Registry: emptyRegistry(), Writer: session.NewWriter(store, chat.ID)})
	if err != nil {
		t.Fatal(err)
	}
	loop.SetExternalSink(make(chan Event, 16))
	var blocked atomic.Bool
	blocked.Store(true)
	var original *invocationBlockedWriter
	var replacement *session.Writer
	var bound []session.MessageReceipt
	loop.SetInvocationPersistence(func(previous SessionWriter) SessionWriter {
		writer := session.NewWriter(store, previous.(interface{ SessionID() string }).SessionID())
		if original == nil {
			original = &invocationBlockedWriter{Writer: writer, blocked: &blocked}
			return original
		}
		replacement = writer
		return writer
	}, invocationReceipt, func(seq int, id int64) { bound = append(bound, session.MessageReceipt{Seq: seq, ID: id}) })
	invocationRun(t, loop, context.Background(), "old failed prompt")
	if len(bound) != 0 || loop.PersistStatus().Pending != 2 || original.FirstUserReceipt() != (session.MessageReceipt{}) {
		t.Fatal("failed foreground prompt published a receipt or lost its pending messages")
	}
	backing := loop.persistHealth.pending
	for _, item := range backing {
		if item.Writer != original {
			t.Fatal("failed message lost its original invocation writer")
		}
	}
	blocked.Store(false)
	invocationRun(t, loop, context.Background(), "current prompt")
	if len(bound) != 1 || bound[0].Seq != 3 || replacement.FirstUserReceipt() != bound[0] || original.FirstUserSeq() != 1 || original.FirstUserReceipt().ID == bound[0].ID {
		t.Fatalf("retry stole the current receipt: bound=%+v original=%+v current=%+v", bound, original.FirstUserReceipt(), replacement.FirstUserReceipt())
	}
	if status := loop.PersistStatus(); status.Pending != 0 || status.Dropped != 0 || !status.LastWriteOK {
		t.Fatalf("rotation lost retry health: %+v", status)
	}
	for _, item := range backing {
		if !reflect.DeepEqual(item, pendingAppend{}) {
			t.Fatal("written retry slot retained message payload or an old Writer")
		}
	}
	rows, err := store.ReadMessages(context.Background(), chat.ID)
	if err != nil || len(rows) != 4 || rows[0].Content != "old failed prompt" || rows[2].Content != "current prompt" {
		t.Fatalf("old and new invocations lost FIFO order: count=%d, error=%v", len(rows), err)
	}
}

func TestInvocationFactoryFollowsResumedSession(t *testing.T) {
	store, first := invocationStore(t)
	second, err := store.Create(first.Cwd, "fixture", "second session")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(context.Background(), second.ID, session.Encoded{Role: "user", Content: "resumed history"}); err != nil {
		t.Fatal(err)
	}
	loop, err := NewLoop(LoopConfig{Provider: makeScriptedProvider("answer"), Registry: emptyRegistry(), Writer: session.NewWriter(store, first.ID)})
	if err != nil {
		t.Fatal(err)
	}
	var receipts []session.MessageReceipt
	loop.SetInvocationPersistence(func(previous SessionWriter) SessionWriter {
		return session.NewWriter(store, previous.(interface{ SessionID() string }).SessionID())
	}, invocationReceipt, func(seq int, id int64) { receipts = append(receipts, session.MessageReceipt{Seq: seq, ID: id}) })
	invocationRun(t, loop, context.Background(), "first session prompt")
	if err := loop.ResumeConversation(context.Background(), session.NewWriter(store, second.ID), []llm.Message{{Role: llm.RoleUser, Content: "resumed history"}}, nil); err != nil {
		t.Fatal(err)
	}
	invocationRun(t, loop, context.Background(), "resumed prompt")
	if len(receipts) != 2 || receipts[1].Seq != 2 || loop.SessionID() != second.ID {
		t.Fatalf("factory retained old SID after resume: %+v", receipts)
	}
	if current, err := store.IsCurrentUserReceipt(context.Background(), second.ID, receipts[1]); err != nil || !current {
		t.Fatalf("resumed receipt belongs to the wrong message: %v", err)
	}
	if current, err := store.IsCurrentUserReceipt(context.Background(), first.ID, receipts[1]); err != nil || current {
		t.Fatal("resumed receipt was written to the old session")
	}
	oldRows, _ := store.ReadMessages(context.Background(), first.ID)
	newRows, _ := store.ReadMessages(context.Background(), second.ID)
	if len(oldRows) != 2 || len(newRows) != 3 || newRows[1].Content != "resumed prompt" {
		t.Fatal("resume/rotation duplicated or cross-wrote transcript history")
	}
}

func TestInvocationPromptRetryDoesNotPublishLateReceipt(t *testing.T) {
	store, chat := invocationStore(t)
	var blocked atomic.Bool
	blocked.Store(true)
	provider := makeScriptedProvider("answer")
	// Recover after the model starts. The initial prompt will be saved by the
	// assistant append's FIFO retry, after its foreground binding opportunity.
	provider.onCalled = func(int) { blocked.Store(false) }
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: emptyRegistry(), Writer: session.NewWriter(store, chat.ID)})
	if err != nil {
		t.Fatal(err)
	}
	loop.SetExternalSink(make(chan Event, 16))
	var writer *invocationBlockedWriter
	bindings := 0
	loop.SetInvocationPersistence(func(previous SessionWriter) SessionWriter {
		writer = &invocationBlockedWriter{Writer: session.NewWriter(store, previous.(interface{ SessionID() string }).SessionID()), blocked: &blocked}
		return writer
	}, invocationReceipt, func(int, int64) { bindings++ })
	invocationRun(t, loop, context.Background(), "retry this prompt")
	if bindings != 0 || writer.FirstUserSeq() != 1 || loop.PersistStatus().Pending != 0 {
		t.Fatal("retry published a receipt after the foreground binding opportunity")
	}
	invocationRun(t, loop, context.Background(), "next prompt")
	if bindings != 1 || writer.FirstUserSeq() != 3 {
		t.Fatal("the next accepted invocation adopted the recovered old prompt")
	}
}

func TestInvocationFactoryDoesNotRunForRejectedRuns(t *testing.T) {
	loop, err := NewLoop(LoopConfig{Provider: makeScriptedProvider("answer"), Registry: emptyRegistry(), Writer: &recordingWriter{}})
	if err != nil {
		t.Fatal(err)
	}
	calls, bindings := 0, 0
	loop.SetInvocationPersistence(func(previous SessionWriter) SessionWriter { calls++; return previous }, func(SessionWriter) (int, int64) { return 1, 1 }, func(int, int64) { bindings++ })
	if _, err := loop.Run(context.Background(), ""); err == nil {
		t.Fatal("empty run accepted")
	}
	loop.sessionBusy.Store(true)
	if _, err := loop.Run(context.Background(), "busy"); err == nil {
		t.Fatal("busy run accepted")
	}
	loop.sessionBusy.Store(false)
	loop.liveContextForRun = func(context.Context) (string, error) { return "", errors.New("synthetic preparation failure") }
	if _, err := loop.Run(context.Background(), "preparation failed"); err == nil {
		t.Fatal("failed preparation accepted")
	}
	if calls != 0 || bindings != 0 {
		t.Fatal("rejected run allocated an invocation Writer or published a receipt")
	}
}

func TestInvocationWriterRebindsDerivedOutputsAndKeepsExplicitOverride(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		previous, next, external := &bindingWriter{}, &bindingWriter{}, &bindingWriter{}
		var override tools.OutputPersistence
		if explicit {
			override = bindingOverride{target: external, nonComparable: []string{"explicit"}}
		}
		loop := bindingLoop(t, previous, override)
		loop.SetInvocationPersistence(func(SessionWriter) SessionWriter { return next }, nil, nil)
		invocationRun(t, loop, context.Background(), "current prompt")
		bindingRun(t, loop)
		if previous.saves != 0 || explicit && (next.saves != 0 || external.saves != 1) || !explicit && (next.saves != 1 || external.saves != 0) {
			t.Fatal("rotation changed configured tool-output ownership")
		}
	}
}
