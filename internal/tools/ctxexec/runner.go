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
	"runtime"
	"strings"
	"time"

	"supercli/internal/system/childproc"
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

	// ExecutablePath is overridable for tests. It is used only to look for a
	// bundled ripgrep binary next to SuperCli (or in its bin/tools directory)
	// when rg is absent from PATH.
	ExecutablePath func() (string, error)

	// Now is overridable for tests. Default = time.Now.
	Now func() time.Time
}

// New returns a Runner bound to the F7 home directory.
func New(home string) *Runner {
	return &Runner{
		home:           home,
		LookPath:       exec.LookPath,
		ExecutablePath: os.Executable,
		Now:            time.Now,
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

	// Resolve the binary directly, never through cmd/PowerShell. In addition to
	// PATH, rg may be bundled beside the GUI executable because desktop apps do
	// not always inherit the user's terminal PATH.
	binary, err := r.resolveBinary(req.Command[0])
	if err != nil {
		return &Result{
			ExitCode: ExitNotFound,
			Command:  req.String(),
			Workdir:  wd,
			Error:    missingBinaryMessage(req.Command[0]),
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
	childproc.HideWindow(cmd)
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

func (r *Runner) resolveBinary(file string) (string, error) {
	lookPath := r.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	binary, pathErr := lookPath(file)
	if pathErr == nil {
		return binary, nil
	}
	if !isRipgrepCommand(file) {
		return "", pathErr
	}
	executablePath := r.ExecutablePath
	if executablePath == nil {
		executablePath = os.Executable
	}
	executable, err := executablePath()
	if err != nil || strings.TrimSpace(executable) == "" {
		return "", pathErr
	}
	base := filepath.Dir(executable)
	for _, rel := range []string{
		"rg.exe", "rg",
		filepath.Join("bin", "rg.exe"), filepath.Join("bin", "rg"),
		filepath.Join("tools", "rg.exe"), filepath.Join("tools", "rg"),
		filepath.Join("bin", "tools", "rg.exe"), filepath.Join("bin", "tools", "rg"),
	} {
		candidate := filepath.Join(base, rel)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || !info.Mode().IsRegular() {
			continue
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
			continue
		}
		return candidate, nil
	}
	return "", pathErr
}

func isRipgrepCommand(file string) bool {
	file = strings.TrimSpace(file)
	return file == "rg" || strings.EqualFold(file, "rg.exe")
}

func missingBinaryMessage(file string) string {
	if isRipgrepCommand(file) {
		return "executable not found: rg; use the built-in search_code tool for file and content searches (no rg installation required)"
	}
	return fmt.Sprintf("executable not found: %s%s", file, WindowsShellHint())
}

// WindowsShellHint names the host and the one shell it has, for the two
// failures that are really "the model assumed POSIX": a missing coreutils
// binary and cmd's 9009. Empty everywhere else, so nothing is paid for it on
// Linux or macOS, and nothing at all is paid on the success path.
func WindowsShellHint() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	return "; windows host: no POSIX tools or pipes, run them via powershell -Command \"...\""
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
	out := append([]string(nil), base...)
	if runtime.GOOS == "windows" {
		// Python otherwise inherits the legacy console code page (for example
		// CP1250) and can crash merely by printing documentation containing
		// non-European characters. These variables are ignored by other tools.
		out = replaceEnv(out, "PYTHONIOENCODING=utf-8")
		out = replaceEnv(out, "PYTHONUTF8=1")
	}
	// For each extra, replace any existing key.
	for _, kv := range extras {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		out = replaceEnv(out, kv)
	}
	return out
}

func replaceEnv(env []string, kv string) []string {
	i := strings.IndexByte(kv, '=')
	if i < 0 {
		return env
	}
	key := kv[:i]
	for j := len(env) - 1; j >= 0; j-- {
		eq := strings.IndexByte(env[j], '=')
		if eq < 0 || !envNamesEqual(env[j][:eq], key) {
			continue
		}
		env = append(env[:j], env[j+1:]...)
	}
	return append(env, kv)
}

func envNamesEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// ensureCompileTimeInterfaceCheck keeps a few stdlib
// imports live even when this file is the only one
// in the package.
var _ = bytes.NewBuffer
var _ = filepath.Join
