package processsession

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestWaitReadyUsesLogEventsAndPreservesEvidence(t *testing.T) {
	tool, item, _ := waitFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observed := &waitObservedContext{Context: ctx, entered: make(chan struct{})}
	results := make(chan snapshot, 1)
	go func() {
		snap, err := tool.Manager.WaitReady(observed, item.id, "READY [0-9]+", 0, 0)
		if err != nil {
			t.Error(err)
		}
		results <- snap
	}()
	select {
	case <-observed.entered:
	case <-ctx.Done():
		t.Fatal("wait not entered")
	}
	_, _ = item.stderr.Write([]byte("initialization\nREA"))
	_, _ = item.stderr.Write([]byte("DY 123\n"))
	select {
	case snap := <-results:
		if !snap.Ready || snap.Status != "running" || snap.ReadyAt == "" || !strings.Contains(snap.Stderr, "READY 123") {
			t.Fatalf("no evidence: %+v", snap)
		}
		if !strings.Contains(snap.StatusSummary, "past evidence") {
			t.Fatal("readiness misrepresented as health monitoring")
		}
	case <-ctx.Done():
		t.Fatal("log writes did not wake readiness")
	}
	finishWaitFixture(item, "failed", 7)
	final, _ := tool.Manager.Wait(context.Background(), item.id)
	if final.Ready || !strings.Contains(final.StatusSummary, "code 7 after") || final.ReadyAt == "" || !strings.Contains(final.StatusSummary, "Past readiness observation") {
		t.Fatalf("exit did not invalidate ready: %+v", final)
	}
}

func TestWaitReadyCancellationAndDeadlineKeepProcessAndOutput(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		tool, item, _ := waitFixture(t)
		item.stdout.Write([]byte("still initializing"))
		ctx, cancel := context.WithCancel(context.Background())
		if !deadline {
			cancel()
		}
		defer cancel()
		ms := 0
		want := context.Canceled
		if deadline {
			ms = 10
			want = context.DeadlineExceeded
		}
		_, err := tool.Manager.WaitReady(ctx, item.id, "READY", 0, ms)
		if !errors.Is(err, want) || !item.running() || item.stdoutCursor != 0 || item.stopRequested {
			t.Fatalf("wait changed process: %v", err)
		}
	}
}

func TestWaitReadyFailsWhenProcessExitsEvenWithMatchingFinalLog(t *testing.T) {
	for _, code := range []int{0, 7} {
		tool, item, _ := waitFixture(t)
		item.stdout.Write([]byte("READY\n"))
		finishWaitFixture(item, "done", code)
		snap, err := tool.Manager.WaitReady(context.Background(), item.id, "READY", 0, 0)
		if err == nil || snap.Ready || snap.ExitCode == nil || *snap.ExitCode != code {
			t.Fatalf("exit mistaken for readiness: %+v %v", snap, err)
		}
	}
}

func TestWaitReadyPortCheckAndPreview(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	tool, item, reg := waitFixture(t)
	item.stdout.Write([]byte("READY\n" + strings.Repeat("output\n", 2000)))
	port := listener.Addr().(*net.TCPAddr).Port
	raw, _ := json.Marshal(map[string]any{"action": "wait_ready", "id": item.id, "ready_pattern": "READY", "ready_port": port, "ready_timeout_ms": 2000})
	r, err := reg.Execute(context.Background(), "process_session", raw)
	if err != nil || r.Err != nil {
		t.Fatalf("port verification failed: %v %v", err, r.Err)
	}
	var preview snapshot
	body := r.ModelPreview
	if json.Unmarshal([]byte(body), &preview) != nil || !preview.Ready || preview.ReadyPort != port || preview.ReadyAt == "" {
		t.Fatalf("preview lost readiness: %s", body)
	}
	_ = listener.Close()
	_, err = tool.Manager.WaitReady(context.Background(), item.id, "READY", port, 2000)
	if err == nil {
		t.Fatal("closed port accepted")
	}
	if snap, _ := tool.Manager.Poll(item.id); snap.Ready {
		t.Fatal("failed port check left readiness valid")
	}
}

func TestProcessTextChunksDoNotSplitUTF8(t *testing.T) {
	b := newStreamBuffer(64)
	b.Write([]byte("xżółć日本語"))
	var joined strings.Builder
	var cursor int64
	for cursor < int64(len("xżółć日本語")) {
		p, next, _ := b.readTextFrom(cursor, 5)
		if !utf8.Valid(p) || next <= cursor {
			t.Fatalf("bad chunk %x next=%d", p, next)
		}
		joined.Write(p)
		cursor = next
	}
	if joined.String() != "xżółć日本語" {
		t.Fatal("text lost at chunk boundary")
	}
}
