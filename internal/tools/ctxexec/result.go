// Package ctxexec runs a single command in a sandboxed
// context-mode environment and returns the bounded
// stdout/stderr as a Result. The intent is to let the
// agent answer questions about large files without
// loading them into the conversation: it writes a small
// script (grep, awk, python3 -c, jq, ...) and the
// sandbox returns just the answer.
//
// Design notes:
//
//   - One process per call. No shell, no pipe, no
//     history. `exec.CommandContext` runs the binary
//     directly so there is no shell-injection surface.
//   - The workdir is checked against the F7 sandbox
//     (ResolveSafe) before exec, so a model that asks
//     for `cd ../../etc` gets ErrEscape, not root.
//   - Env is `sandbox.ScrubEnv()`: no API keys, no
//     tokens, no secrets reach the child.
//   - Output is captured to a temp file (not piped) so a
//     process that produces 1 GB does not deadlock on
//     the kernel pipe buffer. The cap is applied AFTER
//     the process exits, when the file is read in
//     chunks.
//   - Wallclock timeout kills the process; partial
//     output is returned with exit code 124 (the
//     `timeout(1)` convention).
package ctxexec

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxCommandLen caps the size of the command string. A
// 4 KB ceiling is generous for `python3 -c` snippets and
// refuses accidental 1 MB prompt injections.
const MaxCommandLen = 4 * 1024

// MaxStdoutKBHard is the hard ceiling for MaxStdoutKB.
// Anything larger is clamped down. 64 KB is still ~16 K
// tokens — more than enough for any sensible answer.
const MaxStdoutKBHard = 64

// MaxTimeoutMSHard is the hard ceiling for TimeoutMS.
// 30 s is a long time for a single command; longer
// workloads belong in the F10+1 background pool.
const MaxTimeoutMSHard = 30_000

// DefaultTimeoutMS is used when TimeoutMS is zero.
const DefaultTimeoutMS = 10_000

// DefaultMaxStdoutKB is used when MaxStdoutKB is zero.
const DefaultMaxStdoutKB = 16

// DefaultMaxStderrKB caps the stderr stream separately
// from stdout. Errors are usually short; the cap keeps
// runaway deprecation warnings from blowing the result.
const DefaultMaxStderrKB = 4

// Exit codes mirrored from `timeout(1)` and POSIX.
const (
	ExitOK              = 0
	ExitTimeout         = 124
	ExitNotFound        = 127
	ExitNotExecutable   = 126
	ExitSandboxError    = -1
	ExitValidationError = -2
)

// Request is what the model sends. All fields are
// optional except Command.
type Request struct {
	// Command is the binary + its args. Either split
	// ("python3", "-c", "...") or a single token
	// ("echo"). Splitting is the caller's job; this
	// package does not reparse.
	Command []string
	// Workdir is relative to the home and resolved via
	// sandbox.ResolveSafe. Empty = home root.
	Workdir string
	// TimeoutMS caps wallclock time. Zero = default.
	TimeoutMS int
	// MaxStdoutKB caps stdout. Zero = default.
	MaxStdoutKB int
	// MaxStderrKB caps stderr. Zero = default.
	MaxStderrKB int
	// EnvExtra is appended to the scrubbed env. Rarely
	// used; the agent sets the language path through
	// here if needed.
	EnvExtra []string
}

// Result is what the tool returns. Always populated,
// even on errors, so the model can see *why* the
// command failed (missing interpreter, timeout, etc.).
type Result struct {
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	ExitCode        int    `json:"exit_code"`
	TruncatedStdout bool   `json:"truncated_stdout"`
	TruncatedStderr bool   `json:"truncated_stderr"`
	DurationMS      int64  `json:"duration_ms"`
	Command         string `json:"command"` // echoed, joined with spaces
	Workdir         string `json:"workdir"`
	// Error is set only for sandbox / Go-level errors
	// (path escape, nil binary, etc.). The ExitCode
	// mirrors the error class so the model can react.
	Error string `json:"error,omitempty"`
}

// Error sentinels. Use errors.Is.
var (
	ErrEmptyCommand   = errors.New("ctxexec: command is empty")
	ErrCommandTooLong = errors.New("ctxexec: command string too long")
	ErrNoWorkdir      = errors.New("ctxexec: workdir not set")
)

// Validate returns nil if the Request is well-formed.
// It does NOT touch the filesystem; ResolveSafe in the
// Runner does that.
func (r *Request) Validate() error {
	if r == nil {
		return ErrEmptyCommand
	}
	if len(r.Command) == 0 {
		return ErrEmptyCommand
	}
	for _, a := range r.Command {
		if len(a) > MaxCommandLen {
			return ErrCommandTooLong
		}
	}
	return nil
}

// String returns the command as a single shell-shaped
// line for display. It is NOT a safe shell-escape — the
// runner does not go through a shell.
func (r *Request) String() string {
	if r == nil {
		return ""
	}
	return strings.Join(r.Command, " ")
}

// FormatError returns a one-line error string the model
// can read. Empty when there is no error.
func (r *Result) FormatError() string {
	if r == nil || r.Error == "" {
		return ""
	}
	if r.Stderr != "" {
		return fmt.Sprintf("%s: %s", r.Error, strings.TrimSpace(r.Stderr))
	}
	return r.Error
}

// FailTailBytes caps how much of each captured stream is
// inlined into FailureSummary. The runner already caps the
// streams (MaxStderrKB / MaxStdoutKB); this tighter cap
// keeps the ERROR message itself lean — the model usually
// only needs the last error lines to self-correct.
const FailTailBytes = 2048

// FailureSummary renders a failed run as a compact,
// deterministic message for the model. First line carries
// only facts the CLI is sure of (exit code, wallclock,
// timeout); then the stderr/stdout tails so the model can
// fix the command in one turn instead of guessing:
//
//	command_failed exit=1 (1.3s)
//	stderr:
//	FAIL: TestFoo ...
//
// Timeouts are marked explicitly; truncated tails carry a
// marker. No cause guessing — streams are quoted verbatim.
func (r *Result) FailureSummary() string {
	var b strings.Builder
	switch {
	case r.ExitCode == ExitTimeout:
		fmt.Fprintf(&b, "command_failed timeout exit=%d (%s)", r.ExitCode, fmtDurMS(r.DurationMS))
	case r.Error != "":
		fmt.Fprintf(&b, "command_failed exit=%d: %s", r.ExitCode, r.Error)
	default:
		fmt.Fprintf(&b, "command_failed exit=%d (%s)", r.ExitCode, fmtDurMS(r.DurationMS))
	}
	appendTail(&b, "stderr", r.Stderr, r.TruncatedStderr)
	appendTail(&b, "stdout", r.Stdout, r.TruncatedStdout)
	return b.String()
}

// appendTail writes a labelled stream tail, capped at
// FailTailBytes with a truncation marker (also set when the
// runner already truncated the stream at capture time).
func appendTail(b *strings.Builder, label, s string, preTruncated bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	truncated := preTruncated
	if len(s) > FailTailBytes {
		s = tailUTF8(s, FailTailBytes)
		truncated = true
	}
	if truncated {
		label += " (tail, truncated)"
	}
	fmt.Fprintf(b, "\n%s:\n%s", label, s)
}

// tailUTF8 returns the last max bytes of s, moving the cut
// forward (at most 3 bytes) to the next rune boundary so a
// multi-byte character is never split in half. s must be
// longer than max.
func tailUTF8(s string, max int) string {
	i := len(s) - max
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return s[i:]
}

// fmtDurMS renders milliseconds as "1.3s".
func fmtDurMS(ms int64) string {
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}
