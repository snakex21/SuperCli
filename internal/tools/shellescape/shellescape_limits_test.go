package shellescape

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunnerContext_NoOwnDeadline(t *testing.T) {
	for _, timeout := range []time.Duration{NewRunner("").Timeout, 0, -time.Second} {
		parent, cancelParent := context.WithCancel(context.Background())
		ctx, cancel := runnerContext(parent, timeout)
		if ctx != parent {
			t.Errorf("timeout %v wrapped the caller context", timeout)
		}
		if deadline, ok := ctx.Deadline(); ok {
			t.Errorf("timeout %v added a deadline: %v", timeout, deadline)
		}
		cancelParent()
		if ctx.Err() != context.Canceled {
			t.Errorf("timeout %v lost caller cancellation: %v", timeout, ctx.Err())
		}
		cancel()
	}
}

func TestRunnerContext_ExplicitTimeout(t *testing.T) {
	before := time.Now()
	ctx, cancel := runnerContext(context.Background(), time.Minute)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || deadline.Before(before.Add(time.Minute)) || deadline.After(time.Now().Add(time.Minute)) {
		t.Fatalf("explicit timeout deadline = %v, present = %v", deadline, ok)
	}

	parent, cancelParent := context.WithDeadline(context.Background(), before.Add(30*time.Second))
	defer cancelParent()
	ctx, cancel = runnerContext(parent, time.Minute)
	defer cancel()
	if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(before.Add(30*time.Second)) {
		t.Errorf("earlier caller deadline = %v, present = %v", deadline, ok)
	}
}

func TestRunner_CallerCancelKillsProcessTree(t *testing.T) {
	r := NewRunner(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.AfterFunc(200*time.Millisecond, cancel)
	defer timer.Stop()

	start := time.Now()
	res := r.Run(ctx, shellHelperCommand(t, "wait"))
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("caller cancellation took %v, want < 3s", elapsed)
	}
	if res.ExitCode == 0 {
		t.Error("caller cancellation returned exit 0")
	}
	if strings.Contains(strings.ToLower(res.Error), "timeout") {
		t.Errorf("caller cancellation mislabeled as a timeout: %q", res.Error)
	}
}

func TestRunner_CallerDeadline(t *testing.T) {
	r := NewRunner(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	res := r.Run(ctx, shellHelperCommand(t, "wait"))
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("caller deadline took %v, want < 3s", elapsed)
	}
	if res.ExitCode == 0 || !strings.Contains(res.Error, "timeout after") {
		t.Errorf("caller deadline result: exit=%d error=%q", res.ExitCode, res.Error)
	}
	if strings.Contains(res.Error, "after 0s:") {
		t.Errorf("caller deadline reported the unset runner limit: %q", res.Error)
	}
}

func TestRunner_OutputFloodBounded(t *testing.T) {
	r := NewRunner(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res := r.Run(ctx, shellHelperCommand(t, "flood"))
	if res.ExitCode != 0 || res.Error != "" {
		t.Fatalf("flood result: exit=%d error=%q stderr=%q", res.ExitCode, res.Error, res.Stderr)
	}
	for _, stream := range []struct {
		name   string
		output string
		budget int
	}{
		{"stdout", res.Stdout, stdoutHeadBytes + stdoutTailBytes},
		{"stderr", res.Stderr, stderrHeadBytes + stderrTailBytes},
	} {
		if len(stream.output) > stream.budget+128 {
			t.Errorf("%s captured %d bytes, budget=%d plus marker", stream.name, len(stream.output), stream.budget)
		}
		if !strings.HasPrefix(stream.output, stream.name+"-head\n") || !strings.HasSuffix(stream.output, "\n"+stream.name+"-tail\n") {
			t.Errorf("%s lost the first or last output lines", stream.name)
		}
		if strings.Count(stream.output, "omitted_bytes=") != 1 || strings.Contains(stream.output, "truncated at") {
			t.Errorf("%s must carry exactly one omission marker", stream.name)
		}
	}
}

// Reuse the test executable so flood/cancellation coverage needs no Python,
// PowerShell, or other helper runtime. The runner's working directory is TempDir.
func shellHelperCommand(t *testing.T, mode string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("test executable: %v", err)
	}
	args := " -test.run=TestRunnerHelperProcess -- --shellescape-helper " + mode
	if runtime.GOOS == "windows" {
		// cmd /c needs an outer quote around a quoted executable plus arguments.
		return `""` + executable + `"` + args + `"`
	}
	return "'" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "'" + args
}

func TestRunnerHelperProcess(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != "--shellescape-helper" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "wait":
		time.Sleep(10 * time.Second)
	case "flood":
		for _, stream := range []struct {
			name   string
			writer io.Writer
			fill   string
		}{
			{"stdout", os.Stdout, "x"},
			{"stderr", os.Stderr, "y"},
		} {
			if _, err := fmt.Fprintln(stream.writer, stream.name+"-head"); err != nil {
				t.Fatal(err)
			}
			chunk := strings.Repeat(stream.fill, 32*1024)
			for i := 0; i < 256; i++ {
				if _, err := io.WriteString(stream.writer, chunk); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := fmt.Fprintln(stream.writer, "\n"+stream.name+"-tail"); err != nil {
				t.Fatal(err)
			}
		}
	default:
		t.Fatalf("unknown shellescape helper mode: %q", os.Args[len(os.Args)-1])
	}
	os.Exit(0)
}
