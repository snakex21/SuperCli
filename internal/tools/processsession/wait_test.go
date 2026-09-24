package processsession

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/tools/core"
)

type waitObservedContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *waitObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func waitFixture(t *testing.T) (*Tool, *process, *core.Registry) {
	t.Helper()
	tool := New(t.TempDir())
	item := &process{id: "proc-1", command: []string{"test-runner"}, workdir: tool.BaseDir, status: "running", started: time.Now(), exitCode: -1, done: make(chan struct{}), stdout: newStreamBuffer(maxBufferBytes), stderr: newStreamBuffer(maxBufferBytes)}
	tool.Manager.items[item.id] = item
	reg := core.NewRegistry()
	reg.MustRegister(tool.Spec())
	return tool, item, reg
}

func finishWaitFixture(item *process, status string, code int) {
	item.mu.Lock()
	item.status = status
	item.exitCode = code
	item.ended = time.Now()
	item.mu.Unlock()
	close(item.done)
}

func TestWaitReturnsFinalOutputOnlyAfterCompletion(t *testing.T) {
	_, item, reg := waitFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observed := &waitObservedContext{Context: ctx, entered: make(chan struct{})}
	results := make(chan core.Result, 1)
	go func() {
		r, err := reg.Execute(observed, "process_session", json.RawMessage(`{"action":"wait","id":"proc-1"}`))
		if err != nil {
			r.Err = err
		}
		results <- r
	}()
	select {
	case <-observed.entered:
	case <-ctx.Done():
		t.Fatal("wait did not start")
	}
	select {
	case r := <-results:
		t.Fatalf("wait returned before completion: %+v", r)
	default:
	}
	// The diagnostic is past the old 12 KiB poll chunk. Return all retained data,
	// then let the shared output store provide the bounded model-facing view.
	output := strings.Repeat("build output\n", 2000) + "FINAL_BUILD_RESULT"
	item.stdout.Write([]byte(output))
	item.stderr.Write([]byte("final warning"))
	finishWaitFixture(item, "done", 0)
	var result core.Result
	select {
	case result = <-results:
	case <-ctx.Done():
		t.Fatal("completion did not wake wait")
	}
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	var snap snapshot
	if err := json.Unmarshal([]byte(result.Text), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Status != "done" || snap.ExitCode == nil || *snap.ExitCode != 0 || snap.Stdout != output || snap.Stderr != "final warning" {
		t.Fatalf("incomplete result: %+v", snap)
	}
	view := reg.ModelResultContent("process_session", result)
	if !strings.Contains(view, "handle=") || !strings.Contains(view, "FINAL_BUILD_RESULT") || len(view) > 8192 {
		t.Fatalf("unbounded or incomplete model preview: %s", view)
	}
	again, err := reg.Execute(ctx, "process_session", json.RawMessage(`{"action":"wait","id":"proc-1"}`))
	if err != nil || again.Err != nil {
		t.Fatalf("completed wait failed: %+v %v", again, err)
	}
	if strings.Contains(again.Text, "FINAL_BUILD_RESULT") {
		t.Fatal("wait replayed already consumed output")
	}
}

func TestCancelledWaitPreservesProcessAndUnreadOutput(t *testing.T) {
	tool, item, reg := waitFixture(t)
	item.stdout.Write([]byte("unread before cancellation"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &waitObservedContext{Context: ctx, entered: make(chan struct{})}
	resultCh := make(chan core.Result, 1)
	go func() {
		r, err := reg.Execute(observed, "process_session", json.RawMessage(`{"action":"wait","id":"proc-1"}`))
		if err != nil {
			r.Err = err
		}
		resultCh <- r
	}()
	select {
	case <-observed.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not start")
	}
	cancel()
	var result core.Result
	select {
	case result = <-resultCh:
	case <-time.After(5 * time.Second):
		t.Fatal("wait ignored cancellation")
	}
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("wrong cancellation: %+v", result)
	}
	if !item.running() || item.stdoutCursor != 0 || item.stderrCursor != 0 || item.stopRequested {
		t.Fatal("cancelled wait changed process or consumed output")
	}
	finishWaitFixture(item, "done", 0)
	final := waitCompletion(t, tool, item.id)
	if final.Stdout != "unread before cancellation" {
		t.Fatalf("cancelled wait lost output: %+v", final)
	}
}

func TestWaitFailuresAndMissingSessions(t *testing.T) {
	for _, status := range []string{"failed", "timeout"} {
		t.Run(status, func(t *testing.T) {
			_, item, reg := waitFixture(t)
			item.stderr.Write([]byte("specific failing assertion"))
			finishWaitFixture(item, status, 7)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := reg.Execute(ctx, "process_session", json.RawMessage(`{"action":"wait","id":"proc-1"}`))
			want := "exit=7"
			if status == "timeout" {
				want = "timeout exit=124"
			}
			if err != nil || result.Err == nil || !strings.Contains(result.ModelContent(), want) || !strings.Contains(result.ModelContent(), "specific failing assertion") {
				t.Fatalf("failure lost: %+v %v", result, err)
			}
		})
	}
	tool, _, _ := waitFixture(t)
	for _, id := range []string{"", "missing"} {
		if _, err := tool.Manager.Wait(context.Background(), id); err == nil {
			t.Fatalf("accepted id %q", id)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Manager.Wait(ctx, "proc-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("already cancelled context: %v", err)
	}
}

func TestWaitReportsRetainedOutputLimit(t *testing.T) {
	tool, item, _ := waitFixture(t)
	output := strings.Repeat("x", maxBufferBytes+100) + "END"
	item.stdout.Write([]byte(output))
	finishWaitFixture(item, "done", 0)
	final := waitCompletion(t, tool, item.id)
	if len(final.Stdout) != maxBufferBytes || final.OmittedOut != 103 || !strings.HasSuffix(final.Stdout, "END") {
		t.Fatalf("wrong final retention: bytes=%d omitted=%d", len(final.Stdout), final.OmittedOut)
	}
}

func TestManagerCloseWakesWait(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	start, err := execute(t, tool, map[string]any{"action": "start", "command": helperCommand("sleep"), "env": []string{"SUPERCLI_PROCESS_HELPER=1"}, "yield_ms": 0})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observed := &waitObservedContext{Context: ctx, entered: make(chan struct{})}
	results := make(chan snapshot, 1)
	go func() { snap, _ := tool.Manager.Wait(observed, start.ID); results <- snap }()
	select {
	case <-observed.entered:
	case <-ctx.Done():
		t.Fatal("wait did not start")
	}
	tool.Close()
	select {
	case snap := <-results:
		if snap.Status != "stopped" {
			t.Fatalf("close result: %+v", snap)
		}
	case <-ctx.Done():
		t.Fatal("close did not wake wait")
	}
}
