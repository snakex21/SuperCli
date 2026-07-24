// Package shellescape handles the "!command" prefix that lets
// users run shell commands directly from the TUI without
// leaving the conversation. Commands run through the system
// shell (sh -c on Unix, cmd /c on Windows) with a timeout
// and output cap for safety.
package shellescape

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"supercli/internal/system/childproc"
)

// waitDelay bounds how long Wait may block on output pipes that a killed child
// left open. Killing the process tree normally closes them at once; this is the
// backstop for the cases it cannot cover - a Job Object that Windows refused to
// create, or a grandchild that was started in the microseconds between
// CreateProcess and the job assignment.
const waitDelay = time.Second

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
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Shell selection and, on Windows, the raw command-line handling live in
	// shell_windows.go / shell_unix.go.
	cmd := newShellCmd(ctx, command)
	if r.Home != "" {
		cmd.Dir = r.Home
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// On the deadline, kill the whole process tree rather than just the shell.
	// exec's default cancel kills the shell only; a grandchild it spawned keeps
	// the inherited stdout/stderr handles open and Wait blocks on them, so the
	// timeout was not enforced at all ("ping -n 30" returned after 29 s under a
	// 200 ms deadline). The scope is published under a mutex because exec starts
	// its cancel watchdog inside Start, before Start has returned the scope; a
	// deadline firing in that window falls back to killing the shell, and
	// WaitDelay still bounds the wait.
	var mu sync.Mutex
	var scope *childproc.Scope
	cmd.Cancel = func() error {
		mu.Lock()
		s := scope
		mu.Unlock()
		return killShellTree(cmd, s)
	}
	cmd.WaitDelay = waitDelay

	start := time.Now()
	var err error
	started, startErr := childproc.Start(cmd)
	if startErr != nil {
		err = startErr
	} else {
		mu.Lock()
		scope = started
		mu.Unlock()
		err = cmd.Wait()
		_ = started.Close()
	}
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
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = -1
			res.Error = err.Error()
		}
	}

	// A killed command used to surface as exit code 1 with an empty Error, which
	// reads exactly like an ordinary failure - so the model re-ran the identical
	// command and hit the same wall. Say what happened and what to do instead.
	// Only a real deadline qualifies, and only when the command actually failed:
	// one that finished cleanly in the same instant the deadline fired was not
	// killed and must not be labelled as if it had been. A plain non-zero exit is
	// left untouched.
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		limit := timeout
		if parent.Err() != nil {
			// The caller's own deadline fired first; report what was actually
			// spent rather than this runner's unused limit.
			limit = duration.Round(10 * time.Millisecond)
		}
		res.Error = fmt.Sprintf(
			"timeout after %s: command and its child processes were killed. "+
				"Re-running it unchanged will time out again - narrow the work or run it in the background.",
			limit,
		)
		if res.ExitCode == 0 {
			res.ExitCode = -1
		}
	}

	return res
}
