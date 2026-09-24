package ctxexec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestCaptureHelper(t *testing.T) {
	mode := os.Getenv("SUPERCLI_CAPTURE_HELPER")
	if mode == "" {
		return
	}
	if mode == "descendant" {
		time.Sleep(5 * time.Second)
		os.Exit(0)
	}
	if mode == "inherited" {
		child := exec.Command(os.Args[0], "-test.run=^TestCaptureHelper$")
		child.Env = append(os.Environ(), "SUPERCLI_CAPTURE_HELPER=descendant")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			panic(err)
		}
		fmt.Println(child.Process.Pid)
		os.Exit(0)
	}
	count := 6000
	if mode == "overflow" {
		count = 150000
	}
	if mode == "small" {
		count = 1
	}
	for _, stream := range []*os.File{os.Stdout, os.Stderr} {
		fmt.Fprint(stream, "EARLY_EVIDENCE\n", strings.Repeat("zażółć gęślą jaźń\n", count), "FINAL_EVIDENCE\n")
	}
	if mode == "timeout" {
		time.Sleep(5 * time.Second)
	}
	if mode == "failure" {
		os.Exit(7)
	}
	os.Exit(0)
}

func TestRunnerRetainsEvidenceBeforePreview(t *testing.T) {
	for _, mode := range []string{"small", "large", "failure", "overflow", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			timeout := 5000
			if mode == "timeout" {
				timeout = 750
			}
			result, err := New(t.TempDir()).Run(context.Background(), &Request{
				Command:     []string{os.Args[0], "-test.run=^TestCaptureHelper$"},
				EnvExtra:    []string{"SUPERCLI_CAPTURE_HELPER=" + mode},
				MaxStdoutKB: 1, MaxStderrKB: 1, TimeoutMS: timeout,
			})
			if err != nil {
				t.Fatal(err)
			}
			wantExit := 0
			if mode == "failure" {
				wantExit = 7
			}
			if mode == "timeout" {
				wantExit = ExitTimeout
			}
			if result.ExitCode != wantExit {
				t.Fatalf("exit=%d error=%s", result.ExitCode, result.Error)
			}
			for _, stream := range []string{result.Stdout, result.Stderr} {
				if len(stream) > 1024 || !utf8.ValidString(stream) || !strings.HasSuffix(stream, "FINAL_EVIDENCE\n") {
					t.Fatalf("bad preview: bytes=%d valid=%v", len(stream), utf8.ValidString(stream))
				}
			}
			if mode == "small" {
				if result.RetainedJSON() != "" || result.TruncatedStdout || result.TruncatedStderr {
					t.Fatal("small output changed")
				}
				return
			}
			if !result.TruncatedStdout || !result.TruncatedStderr {
				t.Fatal("preview truncation missing")
			}
			var retained Result
			if err := json.Unmarshal([]byte(result.RetainedJSON()), &retained); err != nil {
				t.Fatal(err)
			}
			if retained.ExitCode != wantExit || retained.TruncatedStdout != (mode == "overflow") || retained.TruncatedStderr != (mode == "overflow") {
				t.Fatalf("retained metadata incorrect: exit=%d stdoutTruncated=%v stderrTruncated=%v", retained.ExitCode, retained.TruncatedStdout, retained.TruncatedStderr)
			}
			for _, stream := range []string{retained.Stdout, retained.Stderr} {
				if !strings.HasPrefix(stream, "EARLY_EVIDENCE\n") || !strings.HasSuffix(stream, "FINAL_EVIDENCE\n") || !utf8.ValidString(stream) {
					t.Fatal("retained output lost endpoints or split UTF-8")
				}
				if len(stream) > captureStreamBytes+100 {
					t.Fatalf("capture unbounded: %d", len(stream))
				}
				if strings.Contains(stream, "omitted_bytes=") != (mode == "overflow") {
					t.Fatal("capture omission marker incorrect")
				}
			}
			inline, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(inline), "EARLY_EVIDENCE") {
				t.Fatal("retained capture leaked into inline JSON")
			}
		})
	}
}

func TestRunnerBoundsInheritedOutputPipeWait(t *testing.T) {
	result, err := New(t.TempDir()).Run(context.Background(), &Request{
		Command:  []string{os.Args[0], "-test.run=^TestCaptureHelper$"},
		EnvExtra: []string{"SUPERCLI_CAPTURE_HELPER=inherited"}, TimeoutMS: 5000,
	})
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(result.Stdout))
	if err != nil {
		t.Fatalf("descendant PID: %v; output=%q", err, result.Stdout)
	}
	if child, err := os.FindProcess(pid); err == nil {
		defer child.Release()
		defer child.Kill()
	}
	if result.ExitCode == 0 || !strings.Contains(result.Error, "WaitDelay") {
		t.Fatalf("incomplete capture must surface as failure: exit=%d error=%q", result.ExitCode, result.Error)
	}
}
