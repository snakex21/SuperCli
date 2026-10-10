package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	"supercli/internal/system/childproc"
	"supercli/internal/tools"
)

type checkpointWorkerResult struct {
	record *checkpoint.Record
	err    error
}

func checkpointWorkerAwait[T any](t *testing.T, ctx context.Context, event <-chan T) T {
	t.Helper()
	select {
	case value, ok := <-event:
		if !ok {
			t.Fatal("synthetic completion channel closed without a result")
		}
		return value
	case <-ctx.Done():
		t.Fatalf("synthetic completion handshake: %v", ctx.Err())
	}
	var zero T
	return zero
}

func checkpointWorkerSignal(t *testing.T, ctx context.Context, event <-chan struct{}) {
	t.Helper()
	select {
	case <-event: // Handshake signals intentionally close without sending a value.
	case <-ctx.Done():
		t.Fatalf("synthetic completion signal: %v", ctx.Err())
	}
}

func checkpointWorkerNotification(t *testing.T, ctx context.Context, events <-chan Event) {
	t.Helper()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("synthetic event channel closed before worker notification")
			}
			if _, done := event.(WorkerNotificationEvent); done {
				return
			}
		case <-ctx.Done():
			t.Fatalf("synthetic worker notification: %v", ctx.Err())
		}
	}
}

func checkpointWorkerManager(t *testing.T, home, data string) *checkpoint.Manager {
	t.Helper()
	manager, err := checkpoint.Open(home, data)
	if errors.Is(err, checkpoint.ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func checkpointWorkerRead(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("synthetic file bytes mismatch: got=%q want=%q err=%v", got, want, err)
	}
}

// Read only the synthetic fixture's active-ref metadata, never a real store.
func checkpointWorkerActiveRefs(t *testing.T, ctx context.Context, data string) []string {
	t.Helper()
	root := filepath.Join(data, "checkpoints")
	dirs, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		repo := filepath.Join(root, dir.Name(), "objects.git")
		if _, err := os.Stat(filepath.Join(repo, "HEAD")); os.IsNotExist(err) {
			continue
		} else if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, "git", "--git-dir="+repo, "for-each-ref", "--format=%(refname)", "refs/supercli/active/")
		childproc.HideWindow(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("synthetic active refs: %v: %s", err, out)
		}
		refs = append(refs, strings.Fields(string(out))...)
	}
	return refs
}

func TestCheckpointAsyncWorkerDeferredAndRetainedContinuation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	home, data := t.TempDir(), t.TempDir()
	target := filepath.Join(home, "source.txt")
	before, firstAfter, secondAfter := "before\r\n", "first worker\r\n", "continued worker\r\n"
	if err := os.WriteFile(target, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	manager := checkpointWorkerManager(t, home, data)
	firstTurn := manager.NewTurn("synthetic-worker-session", "first async mutation")
	firstTurn.SetUserSeq(1)
	entered, wrote := make(chan struct{}), make(chan struct{})
	allowWrite, allowReturn := make(chan struct{}), make(chan struct{})
	var releaseWrite, releaseReturn sync.Once
	defer releaseWrite.Do(func() { close(allowWrite) })
	defer releaseReturn.Do(func() { close(allowReturn) })
	var writes atomic.Int32
	write := tools.NewWriteFile(home).Spec()
	realWrite := write.Fn
	write.Fn = func(workerCtx context.Context, raw json.RawMessage) (tools.Result, error) {
		if writes.Add(1) != 1 {
			return realWrite(workerCtx, raw)
		}
		close(entered) // Turn.Wrap has already captured and pinned before.
		select {
		case <-allowWrite:
		case <-workerCtx.Done():
			return tools.Result{Err: workerCtx.Err()}, nil
		}
		result, err := realWrite(workerCtx, raw)
		close(wrote)
		select {
		case <-allowReturn:
		case <-workerCtx.Done():
			return tools.Result{Err: workerCtx.Err()}, nil
		}
		return result, err
	}
	base := tools.NewRegistry()
	base.MustRegister(firstTurn.Wrap(write))
	base.MarkAlwaysOn("write_file")
	provider := &stubProvider{name: "synthetic-checkpoint-worker", scripts: [][]llm.Delta{
		mutationFixtureDelta(mutationFixtureCall("first-write", "write_file", map[string]any{"path": "source.txt", "content": firstAfter})),
		mutationFixtureFinal("First synthetic mutation finished."),
		mutationFixtureDelta(mutationFixtureCall("continued-write", "write_file", map[string]any{"path": "source.txt", "content": secondAfter})),
		mutationFixtureFinal("Synthetic continuation finished."),
	}}
	parent, err := NewLoop(LoopConfig{Provider: &stubReplyProvider{name: "synthetic-parent"}, Registry: base, BaseDir: home})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 64)
	parent.SetExternalSink(events)
	specs := NewSubAgentRegistry()
	specs.MustRegister(SubAgent{Name: "code", Description: "synthetic writer", AllowedTools: []string{"write_file"}, MaxSteps: 4})
	task, err := NewAgentTool(specs, parent, base, provider, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	base.MustRegister(firstTurn.Wrap(task.Spec()))
	result, err := base.Execute(ctx, "task", json.RawMessage(`{"agent":"code","prompt":"Perform the synthetic first mutation.","async":true}`))
	if err != nil || result.Err != nil {
		t.Fatalf("start async worker: %v %v", err, result.Err)
	}
	notified, completionScheduled, completionReceived := false, false, false
	completed := make(chan checkpointWorkerResult, 2)
	t.Cleanup(func() {
		releaseWrite.Do(func() { close(allowWrite) })
		releaseReturn.Do(func() { close(allowReturn) })
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if !notified {
			checkpointWorkerNotification(t, cleanupCtx, events)
		}
		if completionScheduled && !completionReceived {
			checkpointWorkerAwait(t, cleanupCtx, completed)
		} else if !completionScheduled {
			if _, err := firstTurn.Complete(cleanupCtx); err != nil {
				t.Errorf("synthetic cleanup checkpoint: %v", err)
			}
		}
	})
	checkpointWorkerSignal(t, ctx, entered)
	checkpointWorkerRead(t, target, before)
	refs := checkpointWorkerActiveRefs(t, ctx, data)
	if len(refs) != 1 || !strings.HasSuffix(refs[0], "/before") {
		t.Fatalf("before capture did not leave exactly one active before ref: %v", refs)
	}
	foreground := make(chan checkpointWorkerResult, 1)
	foregroundCtx, cancelForeground := context.WithCancel(ctx)
	defer cancelForeground()
	done := func(record *checkpoint.Record, err error) {
		completed <- checkpointWorkerResult{record: record, err: err}
	}
	go func() {
		record, err := firstTurn.CompleteDeferred(foregroundCtx, done)
		foreground <- checkpointWorkerResult{record: record, err: err}
	}()
	returned := checkpointWorkerAwait(t, ctx, foreground)
	completionScheduled = true
	if returned.record != nil || returned.err != nil {
		t.Fatalf("pending async turn did not immediately defer foreground: %+v", returned)
	}
	cancelForeground() // Mirrors UI cleanup; deferred completion needs a fresh ctx.
	if _, err := firstTurn.CompleteDeferred(ctx, done); err != nil {
		t.Fatal(err)
	}
	releaseWrite.Do(func() { close(allowWrite) })
	checkpointWorkerSignal(t, ctx, wrote)
	checkpointWorkerRead(t, target, firstAfter)
	select {
	case early := <-completed:
		t.Fatalf("after captured before actual Fn returned: %+v", early)
	default:
	}
	if manager.Latest("synthetic-worker-session") != nil {
		t.Fatal("checkpoint append happened before actual Fn returned")
	}
	refs = checkpointWorkerActiveRefs(t, ctx, data)
	if len(refs) != 1 || !strings.HasSuffix(refs[0], "/before") {
		t.Fatalf("after snapshot was published inside an unfinished Fn: %v", refs)
	}
	releaseReturn.Do(func() { close(allowReturn) })
	first := checkpointWorkerAwait(t, ctx, completed)
	completionReceived = true
	if first.err != nil || first.record == nil || first.record.Before == first.record.After || !first.record.RawBytes || first.record.UserSeq != 1 {
		t.Fatalf("deferred first checkpoint: %+v", first)
	}
	checkpointWorkerNotification(t, ctx, events)
	notified = true
	select {
	case duplicate := <-completed:
		t.Fatalf("repeated terminal completion delivered twice: %+v", duplicate)
	default:
	}
	if refs := checkpointWorkerActiveRefs(t, ctx, data); len(refs) != 0 {
		t.Fatalf("successful deferred completion retained active pins: %v", refs)
	}
	worker, ok := task.Workers.Get("worker-1")
	if !ok || worker.Snapshot().Status != "done" {
		t.Fatal("async worker was not retained after completion")
	}
	secondTurn := manager.NewTurn("synthetic-worker-session", "resume retained worker")
	secondTurn.SetUserSeq(2)
	current := tools.NewRegistry()
	current.MustRegister(secondTurn.Wrap(NewSendMessageTool(task.Workers).Spec()))
	result, err = current.Execute(ctx, "send_message", json.RawMessage(`{"to":"worker-1","message":"Perform the synthetic second mutation."}`))
	if err != nil || result.Err != nil {
		t.Fatalf("retained continuation did not rebind copied file Fn: %v %v", err, result.Err)
	}
	checkpointWorkerRead(t, target, secondAfter)
	continuedDeferred := make(chan checkpointWorkerResult, 1)
	second, err := secondTurn.CompleteDeferred(ctx, func(record *checkpoint.Record, err error) {
		continuedDeferred <- checkpointWorkerResult{record: record, err: err}
	})
	if err != nil || second == nil || second.ID == first.record.ID || second.UserSeq != 2 || second.Before != first.record.After {
		t.Fatalf("second checkpoint: %v %+v", err, second)
	}
	if atomic.LoadInt32(&provider.calls) != 4 || worker.Snapshot().Runs != 2 {
		t.Fatal("checkpoint lifecycle introduced extra model runs")
	}
	select {
	case extra := <-continuedDeferred:
		t.Fatalf("drained synchronous continuation used a deferred callback: %+v", extra)
	default:
	}
	for _, step := range []struct {
		record *checkpoint.Record
		redo   bool
		want   string
	}{
		{second, false, firstAfter},
		{first.record, false, before},
		{first.record, true, firstAfter},
		{second, true, secondAfter},
	} {
		if step.redo {
			_, err = manager.Redo(ctx, step.record.ID)
		} else {
			_, err = manager.Undo(ctx, step.record.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
		checkpointWorkerRead(t, target, step.want)
	}
	if refs := checkpointWorkerActiveRefs(t, ctx, data); len(refs) != 0 {
		t.Fatalf("continued turn retained active pins: %v", refs)
	}
}

func TestCheckpointAsyncAdvisorDoesNotDeferForeground(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	home, data := t.TempDir(), t.TempDir()
	manager := checkpointWorkerManager(t, home, data)
	turn := manager.NewTurn("synthetic-advisor-session", "read-only advisor")
	base := tools.NewRegistry()
	base.MustRegister(tools.NewReadLines(home).Spec())
	base.MustRegister(turn.Wrap(tools.NewWriteFile(home).Spec())) // Present in parent only.
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseModel sync.Once
	defer releaseModel.Do(func() { close(release) })
	provider := &stubProvider{name: "synthetic-advisor", scripts: [][]llm.Delta{
		mutationFixtureFinal("Synthetic read-only findings."),
	}, onCalled: func(call int) {
		if call == 0 {
			close(entered)
			<-release
		}
	}}
	parent, err := NewLoop(LoopConfig{Provider: &stubReplyProvider{name: "synthetic-parent"}, Registry: base, BaseDir: home})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 64)
	parent.SetExternalSink(events)
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	task, err := NewAgentTool(specs, parent, base, provider, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	base.MustRegister(turn.Wrap(task.Spec()))
	result, err := base.Execute(ctx, "task", json.RawMessage(`{"prompt":"Give the synthetic read-only findings.","advise":true,"async":true}`))
	if err != nil || result.Err != nil {
		t.Fatalf("start advisor: %v %v", err, result.Err)
	}
	notified := false
	t.Cleanup(func() {
		releaseModel.Do(func() { close(release) })
		if !notified {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			checkpointWorkerNotification(t, cleanupCtx, events)
		}
	})
	checkpointWorkerSignal(t, ctx, entered)
	worker, ok := task.Workers.Get("worker-1")
	if !ok || checkpoint.HasCheckpointMutators(worker.Loop.registry) {
		t.Fatal("advisor unexpectedly inherited a checkpoint-mutating file tool")
	}
	if _, immediate := turn.Seal(); !immediate {
		t.Fatal("read-only background model blocked foreground completion")
	}
	deferred := make(chan checkpointWorkerResult, 1)
	record, err := turn.CompleteDeferred(ctx, func(record *checkpoint.Record, err error) {
		deferred <- checkpointWorkerResult{record: record, err: err}
	})
	if err != nil || record != nil {
		t.Fatalf("read-only inline completion: %v %+v", err, record)
	}
	releaseModel.Do(func() { close(release) })
	checkpointWorkerNotification(t, ctx, events)
	notified = true
	select {
	case extra := <-deferred:
		t.Fatalf("read-only worker created deferred checkpoint work: %+v", extra)
	default:
	}
	if manager.Latest("synthetic-advisor-session") != nil || len(checkpointWorkerActiveRefs(t, ctx, data)) != 0 || atomic.LoadInt32(&provider.calls) != 1 {
		t.Fatal("advisor created checkpoint pins/records or extra model calls")
	}
}
