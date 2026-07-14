package ctxexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// hasPython reports whether any working Python is on
// PATH. On Windows the Microsoft Store stub at
// `python3.exe` returns 9009 ("not found; install via
// Store") when executed; we use a real interpreter
// detection via `python --version` to skip the stub.
func hasPython() bool {
	for _, name := range []string{"python3", "python", "py"} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		// Probe: a working interpreter should exit 0
		// for `--version`. The Microsoft Store stub
		// opens a Store window instead.
		cmd := exec.Command(p, "--version")
		out, err := cmd.CombinedOutput()
		if err == nil && len(out) > 0 {
			return true
		}
	}
	return false
}

// pythonCmd returns the first working Python
// interpreter as a command. Use after hasPython().
func pythonCmd() ([]string, bool) {
	for _, name := range []string{"python3", "python", "py"} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if _, err := exec.Command(p, "--version").Output(); err == nil {
			return []string{p}, true
		}
		if name == "py" {
			// `py` is a launcher; needs -3 to pick
			// Python 3.
			return []string{p, "-3"}, true
		}
	}
	return nil, false
}

// hasCmd reports whether the platform shell is
// available. Used to run echo on Windows (where echo
// is a cmd builtin, not a standalone binary).
func hasCmd() bool {
	_, err := exec.LookPath("cmd")
	return err == nil
}

func hasSh() bool {
	_, err := exec.LookPath("sh")
	return err == nil
}

func newTestRunner(t *testing.T) *Runner {
	t.Helper()
	dir := t.TempDir()
	return New(dir)
}

func TestRequest_Validate(t *testing.T) {
	cases := []struct {
		name string
		req  *Request
		ok   bool
	}{
		{"nil", nil, false},
		{"empty", &Request{}, false},
		{"ok", &Request{Command: []string{"echo", "hi"}}, true},
		{"long-arg", &Request{Command: []string{"echo", strings.Repeat("x", MaxCommandLen+1)}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.Validate()
			if tc.ok && err != nil {
				t.Errorf("want nil, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

func TestRequest_String(t *testing.T) {
	r := &Request{Command: []string{"python3", "-c", "print(1)"}}
	if got := r.String(); got != "python3 -c print(1)" {
		t.Errorf("got %q", got)
	}
	if (&Request{}).String() != "" {
		t.Error("empty request should be empty string")
	}
}

func TestResult_FormatError(t *testing.T) {
	if (&Result{}).FormatError() != "" {
		t.Error("empty error should be empty")
	}
	r := &Result{Error: "boom", Stderr: "details\n"}
	got := r.FormatError()
	if !strings.Contains(got, "boom") || !strings.Contains(got, "details") {
		t.Errorf("got %q", got)
	}
}

func TestRunner_Run_Hello(t *testing.T) {
	cmd, ok := echoCmd("hi")
	if !ok {
		t.Skip("no echo equivalent on PATH")
	}
	r := newTestRunner(t)
	res, err := r.Run(context.Background(), &Request{Command: cmd})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "hi") {
		t.Errorf("stdout = %q, want contains hi", res.Stdout)
	}
	if res.DurationMS < 0 {
		t.Errorf("duration = %d", res.DurationMS)
	}
}

func TestRunner_Run_PythonSum(t *testing.T) {
	py, ok := pythonCmd()
	if !ok {
		t.Skip("no working python on PATH")
	}
	r := newTestRunner(t)
	cmd := append(py, "-c", "print(sum(range(10)))")
	res, err := r.Run(context.Background(), &Request{Command: cmd})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "45") {
		t.Errorf("stdout = %q, want 45", res.Stdout)
	}
}

func TestRunner_Run_WorkdirAffectsOutput(t *testing.T) {
	cmd, ok := pwdCmd()
	if !ok {
		t.Skip("no pwd equivalent on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New(dir)
	res, err := r.Run(context.Background(), &Request{Command: cmd})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", res.ExitCode, res.Stderr)
	}
	// pwd should be the home, NOT the OS temp dir.
	if !strings.Contains(res.Stdout, filepathBase(dir)) {
		t.Errorf("stdout = %q, want contains %q", res.Stdout, filepathBase(dir))
	}
}

func TestRunner_Run_Timeout(t *testing.T) {
	cmd, ok := sleepCmd(5)
	if !ok {
		t.Skip("no sleep equivalent on PATH")
	}
	r := newTestRunner(t)
	res, err := r.Run(context.Background(), &Request{
		Command:   cmd,
		TimeoutMS: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != ExitTimeout {
		t.Errorf("exit = %d, want %d", res.ExitCode, ExitTimeout)
	}
	if res.DurationMS > 3000 {
		t.Errorf("duration = %d ms, expected near 500", res.DurationMS)
	}
}

func TestRunner_Run_StdoutCap(t *testing.T) {
	cmd, ok := stdoutFloodCmd()
	if !ok {
		t.Skip("no stdout-flood equivalent on PATH")
	}
	r := newTestRunner(t)
	res, err := r.Run(context.Background(), &Request{
		Command:     cmd,
		MaxStdoutKB: 1, // 1 KB
		TimeoutMS:   1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode == ExitOK {
		t.Errorf("flood should not exit cleanly")
	}
	if !res.TruncatedStdout {
		t.Errorf("TruncatedStdout = false, want true")
	}
	if len(res.Stdout) > 2*1024 {
		t.Errorf("stdout len = %d, want <= 2 KB (1 KB cap + rounding)", len(res.Stdout))
	}
}

func TestRunner_Run_NonZeroExit(t *testing.T) {
	cmd, ok := nonZeroExitCmd()
	if !ok {
		t.Skip("no non-zero equivalent on PATH")
	}
	r := newTestRunner(t)
	res, err := r.Run(context.Background(), &Request{Command: cmd})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode == 0 {
		t.Error("expected non-zero exit")
	}
}

func TestRunner_Run_MissingInterpreter(t *testing.T) {
	r := newTestRunner(t)
	res, err := r.Run(context.Background(), &Request{Command: []string{"definitely_not_a_binary_xyz_42"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != ExitNotFound {
		t.Errorf("exit = %d, want %d (not found)", res.ExitCode, ExitNotFound)
	}
	if !strings.Contains(res.Error, "not found") {
		t.Errorf("error = %q, want contains 'not found'", res.Error)
	}
}

func TestRunner_Run_WorkdirEscape(t *testing.T) {
	r := newTestRunner(t)
	res, err := r.Run(context.Background(), &Request{
		Command: []string{"echo", "hi"},
		Workdir: "../../../etc",
	})
	if err == nil && res.Error == "" {
		t.Fatal("expected escape error")
	}
	if res.ExitCode != ExitSandboxError {
		t.Errorf("exit = %d, want %d", res.ExitCode, ExitSandboxError)
	}
}

func TestRunner_Run_EmptyCommand(t *testing.T) {
	r := newTestRunner(t)
	_, err := r.Run(context.Background(), &Request{})
	if !errors.Is(err, ErrEmptyCommand) {
		t.Errorf("err = %v, want ErrEmptyCommand", err)
	}
}

func TestRunner_Run_Defaults(t *testing.T) {
	// Verify defaults are applied when fields are zero.
	r := newTestRunner(t)
	res, _ := r.Run(context.Background(), &Request{Command: []string{"echo", "x"}})
	if res == nil {
		t.Fatal("nil result")
	}
	if res.Workdir == "" {
		t.Error("Workdir should be set to home")
	}
}

func TestRunner_Run_EnvScrubbed(t *testing.T) {
	cmd, ok := envScrubCheckCmd("OPENAI_FAKE_TEST_KEY_42")
	if !ok {
		t.Skip("no env-check equivalent on PATH")
	}
	t.Setenv("OPENAI_FAKE_TEST_KEY_42", "secret-leak")
	r := newTestRunner(t)
	res, err := r.Run(context.Background(), &Request{Command: cmd})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", res.ExitCode, res.Stderr)
	}
	if strings.Contains(res.Stdout, "secret-leak") {
		t.Errorf("secret leaked into child: %q", res.Stdout)
	}
}

func TestRunner_Run_CustomExtras(t *testing.T) {
	cmd, ok := envCheckCmd("CTXEXEC_TEST")
	if !ok {
		t.Skip("no env-check equivalent on PATH")
	}
	r := newTestRunner(t)
	res, _ := r.Run(context.Background(), &Request{
		Command:  cmd,
		EnvExtra: []string{"CTXEXEC_TEST=hello-from-extra"},
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "hello-from-extra") {
		t.Errorf("extra not passed: stdout=%q", res.Stdout)
	}
}

func TestRunner_Run_GoToolHasUsableCache(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	r := newTestRunner(t)
	res, err := r.Run(context.Background(), &Request{Command: []string{"go", "env", "GOCACHE"}})
	if err != nil {
		t.Fatal(err)
	}
	cache := strings.TrimSpace(res.Stdout)
	if res.ExitCode != 0 || cache == "" || !filepath.IsAbs(cache) {
		t.Fatalf("go cache is unusable in scrubbed environment: exit=%d cache=%q stderr=%q", res.ExitCode, cache, res.Stderr)
	}
}

func TestRunner_Run_NilRunner(t *testing.T) {
	var r *Runner
	_, err := r.Run(context.Background(), &Request{Command: []string{"echo", "hi"}})
	if err == nil {
		t.Error("nil runner should error")
	}
}

func TestRunner_Run_EmptyHome(t *testing.T) {
	r := &Runner{LookPath: exec.LookPath, Now: time.Now}
	_, err := r.Run(context.Background(), &Request{Command: []string{"echo", "hi"}})
	if err == nil {
		t.Error("empty home should error")
	}
}

func TestBuildEnv_ReplacesKey(t *testing.T) {
	out := buildEnv([]string{"PATH=/custom", "NEW=1"})
	// PATH should now be exactly /custom.
	found := false
	for _, kv := range out {
		if kv == "PATH=/custom" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("PATH replacement failed; env=%v", out)
	}
	foundNew := false
	for _, kv := range out {
		if strings.HasPrefix(kv, "NEW=") {
			foundNew = true
			break
		}
	}
	if !foundNew {
		t.Errorf("NEW var not added; env=%v", out)
	}
}

func TestBuildEnv_DropsNoEquals(t *testing.T) {
	out := buildEnv([]string{"NOEQUALS", "OK=1"})
	for _, kv := range out {
		if kv == "NOEQUALS" || strings.HasPrefix(kv, "NOEQUALS=") {
			t.Errorf("malformed extra leaked: %q", kv)
		}
	}
}

// platform helpers -------------------------------------------------------

// echoCmd returns a portable "echo X" command.
// On Unix: `echo` is a binary. On Windows: `cmd /c echo X`.
// Returns (nil, false) when neither is available.
func echoCmd(text string) ([]string, bool) {
	if runtime.GOOS == "windows" {
		if hasCmd() {
			return []string{"cmd", "/c", "echo", text}, true
		}
		return nil, false
	}
	if _, err := exec.LookPath("echo"); err == nil {
		return []string{"echo", text}, true
	}
	return nil, false
}

// pwdCmd returns a portable "pwd" command.
func pwdCmd() ([]string, bool) {
	if runtime.GOOS == "windows" {
		if hasCmd() {
			return []string{"cmd", "/c", "cd"}, true
		}
		return nil, false
	}
	if _, err := exec.LookPath("pwd"); err == nil {
		return []string{"pwd"}, true
	}
	return nil, false
}

// sleepCmd returns a portable "sleep N" command.
// On Unix: `sleep N`. On Windows: `ping -n N 127.0.0.1` is the
// classic substitute (sleeps ~N-1 seconds), but we use
// `timeout N >NUL` which is built into cmd.
func sleepCmd(seconds int) ([]string, bool) {
	if runtime.GOOS == "windows" {
		if hasCmd() {
			// cmd's `timeout` is interactive; use ping
			// for non-interactive sleep.
			return []string{"ping", "-n", itoa(seconds + 1), "127.0.0.1"}, true
		}
		return nil, false
	}
	if _, err := exec.LookPath("sleep"); err == nil {
		return []string{"sleep", itoa(seconds)}, true
	}
	return nil, false
}

// stdoutFloodCmd returns a command that produces
// unbounded stdout. We kill it with the timeout.
func stdoutFloodCmd() ([]string, bool) {
	py, ok := pythonCmd()
	if !ok {
		return nil, false
	}
	// Write a 50 MB stream in 64 KB chunks so the
	// kernel pipe buffer is constantly refilled and
	// the timeout (1s) always wins.
	script := "import sys, time; " +
		"[sys.stdout.write('a' * 65536) or sys.stdout.flush() or time.sleep(0.001) for _ in range(10000)]"
	return append(py, "-u", "-c", script), true
}

// nonZeroExitCmd returns a command that exits 1.
func nonZeroExitCmd() ([]string, bool) {
	if runtime.GOOS == "windows" {
		if hasCmd() {
			return []string{"cmd", "/c", "exit 1"}, true
		}
		return nil, false
	}
	if _, err := exec.LookPath("false"); err == nil {
		return []string{"false"}, true
	}
	return nil, false
}

// envScrubCheckCmd returns a command that prints "LEAK"
// if the env var `name` is set to anything truthy.
// We use cmd's `set` and `findstr` on Windows and
// `env | grep -c` on Unix.
func envScrubCheckCmd(name string) ([]string, bool) {
	if runtime.GOOS == "windows" {
		if hasCmd() {
			// `set NAME=` returns 1 (not found) if absent,
			// and a line with the value if present.
			// Pipe through findstr; if found, leak.
			// We use a guard var: set LEAK=0; if env
			// has the var, set LEAK=1.
			return []string{"cmd", "/c", "set LEAK=0 && (set " + name + " >nul 2>&1 && set LEAK=1) || set LEAK=0 && echo LEAK=%LEAK%"}, true
		}
		return nil, false
	}
	if hasSh() {
		// `env | grep -c '^NAME='` is 0 when not set.
		// We want LEAK=1 when secret is set.
		return []string{"sh", "-c", "if env | grep -q '^" + name + "='; then echo LEAK=1; else echo LEAK=0; fi"}, true
	}
	return nil, false
}

// envCheckCmd returns a command that prints the value
// of the env var `name` (empty if not set).
func envCheckCmd(name string) ([]string, bool) {
	if runtime.GOOS == "windows" {
		if hasCmd() {
			return []string{"cmd", "/c", "echo %" + name + "%"}, true
		}
		return nil, false
	}
	if _, err := exec.LookPath("printenv"); err == nil {
		return []string{"printenv", name}, true
	}
	return nil, false
}

func filepathBase(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

func itoa(n int) string {
	// avoid strconv import in the helpers
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := "0123456789"
	buf := make([]byte, 0, 12)
	for n > 0 {
		buf = append([]byte{digits[n%10]}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
