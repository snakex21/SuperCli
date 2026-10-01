package processsession

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOutputDrainJoinsReadersBeforeCompletion(t *testing.T) {
	p := &process{closeOutput: func() { t.Error("ordinary drain unexpectedly cut output") }}
	p.streams.Add(1)
	finished := make(chan bool, 1)
	go func() { finished <- p.drainOutput() }()
	select {
	case <-finished:
		t.Fatal("drain finished before its reader")
	default:
	}
	p.streams.Done()
	select {
	case incomplete := <-finished:
		if incomplete {
			t.Fatal("ordinary drain marked incomplete")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reader completion did not release drain")
	}
}

func TestOutputDrainClosesHeldReaderWithinGrace(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	defer reader.Close()
	p := &process{stdout: newStreamBuffer(maxBufferBytes), closeOutput: func() { _ = reader.Close() }}
	p.streams.Add(1)
	go func() { defer p.streams.Done(); _, _ = io.Copy(p.stdout, reader) }()
	if _, err := writer.Write([]byte("preserved prefix")); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if !p.drainOutput() {
		t.Fatal("held output pipe was declared complete")
	}
	if time.Since(started) > 4*time.Second {
		t.Fatal("held output pipe exceeded bounded drain")
	}
	data, _, _ := p.stdout.readFrom(0, maxBufferBytes)
	if string(data) != "preserved prefix" {
		t.Fatalf("captured prefix lost: %q", data)
	}
}

func TestInheritedOutputKeepsExitAndWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows Job Object closes descendants; inherited-handle fallback fixture is Unix-specific")
	}
	for _, mode := range []string{"descendant", "descendant-fail"} {
		t.Run(mode, func(t *testing.T) {
			tool := New(t.TempDir())
			t.Cleanup(tool.Close)
			pidFile := filepath.Join(t.TempDir(), "child.pid")
			t.Cleanup(func() {
				raw, err := os.ReadFile(pidFile)
				if err == nil {
					pid, err := strconv.Atoi(string(raw))
					if err == nil {
						if child, err := os.FindProcess(pid); err == nil {
							_ = child.Kill()
							_, _ = child.Wait()
						}
					}
				}
			})
			start, err := tool.Manager.Start(params{Command: helperCommand(mode), Env: []string{"SUPERCLI_PROCESS_HELPER=1", "SUPERCLI_DESCENDANT_PID=" + pidFile}, TimeoutMS: 5000, YieldMS: new(int)})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			final, err := tool.Manager.Wait(ctx, start.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus, wantCode := "done", 0
			if mode == "descendant-fail" {
				wantStatus, wantCode = "failed", 7
			}
			if final.Status != wantStatus || final.ExitCode == nil || *final.ExitCode != wantCode || !final.OutputIncomplete || final.OutputWarning == "" {
				t.Fatalf("exit/capture metadata changed: %+v", final)
			}
			if !strings.Contains(start.Stdout+final.Stdout, "parent completed stdout") || !strings.Contains(start.Stderr+final.Stderr, "parent completed stderr") {
				t.Fatalf("parent evidence missing: %+v %+v", start, final)
			}
			if err := final.commandFailure(); err != nil && !strings.Contains(err.Error(), "incomplete") {
				t.Fatal("failure diagnostic lost capture warning")
			}
			if (final.commandFailure() != nil) != (wantCode != 0) {
				t.Fatal("capture warning changed command success")
			}
			final.Stdout = strings.Repeat("retained output ", 1000)
			var preview snapshot
			if err := json.Unmarshal([]byte(final.modelPreview()), &preview); err != nil || !preview.OutputIncomplete || preview.OutputWarning != final.OutputWarning {
				t.Fatal("model preview lost capture warning")
			}
		})
	}
}

func TestProcessExitIsNotRetimedByOutputDrain(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdinReader.Close()
	defer stdinWriter.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	p := &process{id: "proc-1", stdout: newStreamBuffer(maxBufferBytes), stderr: newStreamBuffer(maxBufferBytes), stdin: stdinWriter, done: make(chan struct{}), cancel: cancel, status: "running", started: time.Now(), closeOutput: func() { _ = reader.Close() }, waitFn: func() (int, error) { return 0, nil }, killFn: func() error { t.Error("already exited fixture was killed"); return nil }}
	p.streams.Add(1)
	go func() { defer p.streams.Done(); _, _ = io.Copy(p.stdout, reader) }()
	p.wait(ctx)
	got := p.snapshot(maxBufferBytes)
	if got.Status != "done" || got.ExitCode == nil || *got.ExitCode != 0 || !got.OutputIncomplete {
		t.Fatalf("output drain changed successful process result: %+v", got)
	}
}

func TestListKeepsIncompleteCaptureWithoutConsumingOutput(t *testing.T) {
	tool, item, _ := waitFixture(t)
	_, _ = item.stdout.Write([]byte("still unread"))
	item.outputIncomplete = true
	finishWaitFixture(item, "done", 0)
	listed := tool.Manager.List()
	if len(listed) != 1 || !listed[0].OutputIncomplete || listed[0].OutputWarning != processOutputWarning {
		t.Fatalf("list lost capture metadata: %+v", listed)
	}
	if item.stdoutCursor != 0 || item.stderrCursor != 0 {
		t.Fatal("list consumed stream output")
	}
	final, err := tool.Manager.Wait(context.Background(), item.id)
	if err != nil || final.Stdout != "still unread" || !final.OutputIncomplete {
		t.Fatalf("list altered subsequent wait: %+v %v", final, err)
	}
}
