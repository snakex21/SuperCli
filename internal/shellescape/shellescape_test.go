package shellescape

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestIsShellEscape_True(t *testing.T) {
	cases := []string{
		"!ls -la",
		"!echo hello",
		"!pwd",
	}
	for _, c := range cases {
		if !IsShellEscape(c) {
			t.Errorf("IsShellEscape(%q) = false, want true", c)
		}
	}
}

func TestIsShellEscape_False(t *testing.T) {
	cases := []string{
		"ls -la",
		"! echo space after bang",
		"",
		"!",
		"hello world",
		"/darwin 3 test",
	}
	for _, c := range cases {
		if IsShellEscape(c) {
			t.Errorf("IsShellEscape(%q) = true, want false", c)
		}
	}
}

func TestExtractCommand(t *testing.T) {
	cases := map[string]string{
		"!ls -la":    "ls -la",
		"!echo hello": "echo hello",
		"! pwd":      "pwd",
	}
	for input, want := range cases {
		got := ExtractCommand(input)
		if got != want {
			t.Errorf("ExtractCommand(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestExtractCommand_NotEscape(t *testing.T) {
	if got := ExtractCommand("hello"); got != "" {
		t.Errorf("ExtractCommand on non-escape = %q", got)
	}
}

func TestRunner_Echo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}
	r := NewRunner(t.TempDir())
	res := r.Run(context.Background(), "echo hello")
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0: %s", res.ExitCode, res.Error)
	}
	if !strings.Contains(res.Stdout, "hello") {
		t.Errorf("stdout = %q, missing 'hello'", res.Stdout)
	}
}

func TestRunner_NonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}
	r := NewRunner(t.TempDir())
	res := r.Run(context.Background(), "exit 42")
	if res.ExitCode != 42 {
		t.Errorf("exit = %d, want 42", res.ExitCode)
	}
}

func TestRunner_EmptyCommand(t *testing.T) {
	r := NewRunner(t.TempDir())
	res := r.Run(context.Background(), "")
	if res.Error == "" {
		t.Fatal("expected error for empty command")
	}
}

func TestRunner_NilRunner(t *testing.T) {
	var r *Runner
	res := r.Run(context.Background(), "echo hi")
	if res.Error == "" {
		t.Fatal("expected error for nil runner")
	}
}

func TestRunner_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}
	r := NewRunner(t.TempDir())
	r.Timeout = 100 * time.Millisecond
	res := r.Run(context.Background(), "sleep 10")
	if res.ExitCode == 0 {
		t.Fatal("expected non-zero exit for timeout")
	}
}

func TestRunner_CwdIsHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}
	dir := t.TempDir()
	r := NewRunner(dir)
	res := r.Run(context.Background(), "pwd")
	if !strings.Contains(res.Stdout, dir) {
		t.Errorf("stdout = %q, expected to contain %q", res.Stdout, dir)
	}
}

func TestRunner_StdoutTruncation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}
	r := NewRunner(t.TempDir())
	// Generate >16 KB of output.
	res := r.Run(context.Background(), "python3 -c \"print('x' * 20000)\"")
	if res.Stdout == "" {
		t.Fatal("expected some stdout")
	}
	// Should be truncated.
	if len(res.Stdout) > 17000 { // 16KB + truncation message
		t.Errorf("stdout too long: %d bytes", len(res.Stdout))
	}
}

func TestResult_Fields(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}
	r := NewRunner(t.TempDir())
	res := r.Run(context.Background(), "echo test")
	if res.Command != "echo test" {
		t.Errorf("Command = %q", res.Command)
	}
	if res.Duration <= 0 {
		t.Errorf("Duration = %v", res.Duration)
	}
}
