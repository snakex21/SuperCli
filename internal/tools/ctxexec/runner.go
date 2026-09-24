package ctxexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"supercli/internal/system/childproc"
	"supercli/internal/tools/core"
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
	configureCommandLine(cmd, req.Command[1:])
	cmd.Dir = wd
	cmd.Env = buildEnv(req.EnvExtra)

	// Drain both streams while the process runs. Retention is bounded in RAM
	// (including for noisy commands), with no temporary files or disk writes.
	stdout := core.NewHeadTailBuffer(captureStreamBytes/2, captureStreamBytes/2)
	stderr := core.NewHeadTailBuffer(captureStreamBytes/2, captureStreamBytes/2)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// A detached descendant may inherit a pipe after the command exits.
	// Bound that wait and report incomplete capture instead of hanging.
	cmd.WaitDelay = time.Second

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

	result := &Result{
		Stdout: stdout.String(), Stderr: stderr.String(),
		ExitCode: exit, DurationMS: dur, Command: req.String(), Workdir: wd,
		TruncatedStdout: stdout.Truncated(), TruncatedStderr: stderr.Truncated(),
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			result.Error = runErr.Error()
		}
	}
	if len(result.Stdout) > maxOut*1024 || len(result.Stderr) > maxErr*1024 {
		retained := *result
		result.retained = &retained
		if len(result.Stdout) > maxOut*1024 {
			result.Stdout = tailUTF8(result.Stdout, maxOut*1024)
			result.TruncatedStdout = true
		}
		if len(result.Stderr) > maxErr*1024 {
			result.Stderr = tailUTF8(result.Stderr, maxErr*1024)
			result.TruncatedStderr = true
		}
	}
	return result, nil
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

// Each stream retains up to 1 MiB of content. Above that, keep the first
// and last halves with an explicit omitted-bytes/lines marker. read_output
// can inspect this capture, while the usual small tail remains inline.
const captureStreamBytes = 1024 * 1024

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
