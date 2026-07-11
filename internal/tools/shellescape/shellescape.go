// Package shellescape handles the "!command" prefix that lets
// users run shell commands directly from the TUI without
// leaving the conversation. Commands run through the system
// shell (sh -c on Unix, cmd /c on Windows) with a timeout
// and output cap for safety.
package shellescape

import (
	"bytes"
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"supercli/internal/system/childproc"
)

// Result holds the output of a shell escape command.
type Result struct {
	Command  string
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
	Error    string // Go-level error (e.g. timeout)
}

// IsShellEscape returns true if text starts with "!" followed
// by a non-space character. The "!" alone is NOT a shell escape.
func IsShellEscape(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "!") && len(text) > 1 && text[1] != ' '
}

// ExtractCommand returns the command string after the "!" prefix.
func ExtractCommand(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "!") {
		return ""
	}
	return strings.TrimSpace(text[1:])
}

// Runner executes shell commands. The home directory is used
// as the working directory. Timeout defaults to 30 seconds.
type Runner struct {
	Home    string
	Timeout time.Duration
}

// NewRunner creates a Runner bound to the given home directory.
func NewRunner(home string) *Runner {
	return &Runner{Home: home, Timeout: 30 * time.Second}
}

// Run executes the command through the system shell and returns
// the captured output. Stdout is capped at 16 KB.
func (r *Runner) Run(ctx context.Context, command string) *Result {
	if r == nil {
		return &Result{Command: command, Error: "nil runner"}
	}
	if strings.TrimSpace(command) == "" {
		return &Result{Command: command, Error: "empty command"}
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var shell, flag string
	if runtime.GOOS == "windows" {
		shell, flag = "cmd", "/c"
	} else {
		shell, flag = "sh", "-c"
	}

	cmd := exec.CommandContext(ctx, shell, flag, command)
	childproc.HideWindow(cmd)
	if r.Home != "" {
		cmd.Dir = r.Home
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	res := &Result{
		Command:  command,
		Duration: duration,
	}

	// Cap stdout at 16 KB.
	out := stdout.Bytes()
	const maxOut = 16 * 1024
	if len(out) > maxOut {
		out = out[:maxOut]
		res.Stdout = string(out) + "\n... (truncated at 16 KB)"
	} else {
		res.Stdout = string(out)
	}

	errStr := stderr.Bytes()
	const maxErr = 4 * 1024
	if len(errStr) > maxErr {
		errStr = errStr[:maxErr]
	}
	res.Stderr = string(errStr)

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = -1
			res.Error = err.Error()
		}
	}

	return res
}
