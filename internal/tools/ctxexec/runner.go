package ctxexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/tools/sandbox"
)

// Runner executes a single command in a sandboxed
// context-mode environment. It is safe for concurrent
// use; the only mutable state is the home path which is
// set at construction.
type Runner struct {
	home string

	// LookPath is overridable for tests. Default =
	// exec.LookPath.
	LookPath func(file string) (string, error)

	// Now is overridable for tests. Default = time.Now.
	Now func() time.Time
}

// New returns a Runner bound to the F7 home directory.
func New(home string) *Runner {
	return &Runner{
		home:     home,
		LookPath: exec.LookPath,
		Now:      time.Now,
	}
}

// Run executes the request and returns a Result. The
// returned Result is always non-nil; check Error /
// ExitCode to see if the run succeeded.
func (r *Runner) Run(parent context.Context, req *Request) (*Result, error) {
	if r == nil {
		return nil, errors.New("ctxexec: nil Runner")
	}
	if err := req.Validate(); err != nil {
		return &Result{
			ExitCode: ExitValidationError,
			Command:  req.String(),
			Error:    err.Error(),
		}, err
	}
	if r.home == "" {
		return &Result{
			ExitCode: ExitSandboxError,
			Command:  req.String(),
			Error:    "ctxexec: empty home",
		}, errors.New("ctxexec: empty home")
	}

	// Sandbox: resolve workdir inside home.
	wd, err := sandbox.ResolveSafe(r.home, req.Workdir)
	if err != nil {
		return &Result{
			ExitCode: ExitSandboxError,
			Command:  req.String(),
			Workdir:  req.Workdir,
			Error:    err.Error(),
		}, err
	}

	// Resolve binary via LookPath. We run the binary
	// directly, not through a shell.
	binary, err := r.LookPath(req.Command[0])
	if err != nil {
		return &Result{
			ExitCode: ExitNotFound,
			Command:  req.String(),
			Workdir:  wd,
			Error:    fmt.Sprintf("interpreter not found: %s", req.Command[0]),
		}, nil
	}

	timeout := req.TimeoutMS
	if timeout <= 0 {
		timeout = DefaultTimeoutMS
	}
	if timeout > MaxTimeoutMSHard {
		timeout = MaxTimeoutMSHard
	}
	maxOut := req.MaxStdoutKB
	if maxOut <= 0 {
		maxOut = DefaultMaxStdoutKB
	}
	if maxOut > MaxStdoutKBHard {
		maxOut = MaxStdoutKBHard
	}
	maxErr := req.MaxStderrKB
	if maxErr <= 0 {
		maxErr = DefaultMaxStderrKB
	}
	if maxErr > MaxStdoutKBHard {
		maxErr = MaxStdoutKBHard
	}

	// Build the command. Args after the binary are
	// passed verbatim. CommandContext takes the
	// TIMEOUT context so the kill goroutine fires
	// when the timeout elapses (or when the caller
	// cancels the parent ctx).
	runCtx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(runCtx, binary, req.Command[1:]...)
	cmd.Dir = wd
	cmd.Env = buildEnv(req.EnvExtra)

	// Capture stdout/stderr to temp files so a 1 GB
	// writer cannot deadlock on a 64 KB pipe buffer.
	stdoutFile, err := os.CreateTemp("", "ctxexec-out-*.txt")
	if err != nil {
		return &Result{ExitCode: ExitSandboxError, Command: req.String(), Workdir: wd,
			Error: "create stdout temp: " + err.Error()}, err
	}
	defer func() {
		stdoutFile.Close()
		os.Remove(stdoutFile.Name())
	}()
	stderrFile, err := os.CreateTemp("", "ctxexec-err-*.txt")
	if err != nil {
		return &Result{ExitCode: ExitSandboxError, Command: req.String(), Workdir: wd,
			Error: "create stderr temp: " + err.Error()}, err
	}
	defer func() {
		stderrFile.Close()
		os.Remove(stderrFile.Name())
	}()
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile

	start := r.Now()
	runErr := cmd.Run()
	dur := r.Now().Sub(start).Milliseconds()

	exit := ExitOK
	if runErr != nil {
		exit = classifyErr(runErr, runCtx.Err())
	}
	// We need the WaitDelay/ExitCode. exec.ExitError
	// exposes it.
	if ee, ok := runErr.(*exec.ExitError); ok {
		exit = ee.ExitCode()
		if runCtx.Err() != nil {
			exit = ExitTimeout
		}
	}

	// Drain captured files. Truncation is applied after
	// the process exits, so we always read what was
	// produced up to kill/exit.
	stdoutBytes, outTrunc := readCapped(stdoutFile.Name(), maxOut*1024)
	stderrBytes, errTrunc := readCapped(stderrFile.Name(), maxErr*1024)

	return &Result{
		Stdout:          string(stdoutBytes),
		Stderr:          string(stderrBytes),
		ExitCode:        exit,
		TruncatedStdout: outTrunc,
		TruncatedStderr: errTrunc,
		DurationMS:      dur,
		Command:         req.String(),
		Workdir:         wd,
	}, nil
}

// classifyErr maps a non-ExitError to one of our exit
// codes. Mostly: context.DeadlineExceeded -> timeout.
func classifyErr(err error, ctxErr error) int {
	if errors.Is(err, context.DeadlineExceeded) || ctxErr == context.DeadlineExceeded {
		return ExitTimeout
	}
	if errors.Is(err, context.Canceled) {
		return ExitSandboxError
	}
	return ExitSandboxError
}

// readCapped reads up to cap bytes from path. If the
// file is longer, the trailing bytes are kept and a
// truncation flag is returned. The file is closed by
// the caller.
func readCapped(path string, cap int) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false
	}
	if st.Size() <= int64(cap) {
		b := make([]byte, st.Size())
		_, _ = io.ReadFull(f, b)
		return b, false
	}
	// Keep the last `cap` bytes — usually what the
	// model wants (errors, end-of-pipe output).
	buf := make([]byte, cap)
	_, err = f.ReadAt(buf, st.Size()-int64(cap))
	if err != nil && err != io.EOF {
		// Fall back to reading from the start.
		_, _ = f.Seek(0, io.SeekStart)
		_, _ = io.ReadFull(f, buf)
	}
	return buf, true
}

// buildEnv composes the scrubbed env with the extras.
// The scrub drops any var matching the F7 patterns
// (keys/tokens/secrets/aws/openai/...). The extras
// layer on top.
func buildEnv(extras []string) []string {
	// Start with a copy of the current process env,
	// then scrub. ScrubEnv operates on a provided env
	// (so tests can pass an empty slice for isolation).
	host := os.Environ()
	base := sandbox.ScrubEnv(host)
	if len(extras) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extras))
	out = append(out, base...)
	// For each extra, replace any existing key.
	for _, kv := range extras {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		k := kv[:i]
		for j, existing := range out {
			if strings.HasPrefix(existing, k+"=") {
				out = append(out[:j], out[j+1:]...)
				break
			}
		}
		out = append(out, kv)
	}
	return out
}

// ensureCompileTimeInterfaceCheck keeps a few stdlib
// imports live even when this file is the only one
// in the package.
var _ = bytes.NewBuffer
var _ = filepath.Join
