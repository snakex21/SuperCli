package workflow

import (
	"context"
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"supercli/internal/tools/ctxexec"
)

func newCtxTestRunner(t *testing.T) *ctxexec.Runner {
	t.Helper()
	return ctxexec.New(t.TempDir())
}

func mustJSON(t *testing.T, s string) json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(s)) {
		t.Fatalf("invalid json: %s", s)
	}
	return json.RawMessage(s)
}

func TestCtxExecute_Spec_Validates(t *testing.T) {
	tool := NewCtxExecuteTool(newCtxTestRunner(t), ".")
	spec := tool.Spec()
	if spec.Name != "ctx_execute" {
		t.Errorf("name = %q", spec.Name)
	}
	if spec.Description == "" {
		t.Error("description is empty")
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
	if !strings.Contains(spec.Schema, "command") {
		t.Error("schema missing command")
	}
}

func TestCtxExecute_Execute_HappyPath(t *testing.T) {
	cmd, ok := echoCmd("hi")
	if !ok {
		t.Skip("no echo equivalent")
	}
	tool := NewCtxExecuteTool(newCtxTestRunner(t), ".")
	res, err := tool.Execute(context.Background(), mustJSON(t, jsonObj(t, map[string]any{
		"command": cmd,
	})))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.Err != nil {
		t.Fatalf("res.Err = %v", res.Err)
	}
	// Text is the JSON of the Result.
	var pr ctxexec.Result
	if err := json.Unmarshal([]byte(res.Text), &pr); err != nil {
		t.Fatalf("bad json in result: %v; text=%q", err, res.Text)
	}
	if pr.ExitCode != 0 {
		t.Errorf("exit = %d", pr.ExitCode)
	}
	if !strings.Contains(pr.Stdout, "hi") {
		t.Errorf("stdout = %q", pr.Stdout)
	}
}

func TestCtxExecute_Execute_BadJSON(t *testing.T) {
	tool := NewCtxExecuteTool(newCtxTestRunner(t), ".")
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"command": [`))
	if err == nil {
		t.Error("expected error for bad json")
	}
}

func TestCtxExecute_Execute_EmptyCommand(t *testing.T) {
	tool := NewCtxExecuteTool(newCtxTestRunner(t), ".")
	_, err := tool.Execute(context.Background(), mustJSON(t, `{"command": []}`))
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestCtxExecute_Execute_EmptyArgInCommand(t *testing.T) {
	tool := NewCtxExecuteTool(newCtxTestRunner(t), ".")
	_, err := tool.Execute(context.Background(), mustJSON(t, `{"command": ["echo", ""]}`))
	if err == nil {
		t.Error("expected error for empty arg")
	}
}

func TestCtxExecute_Execute_NilRunner(t *testing.T) {
	tool := &CtxExecuteTool{Runner: nil, Home: "."}
	_, err := tool.Execute(context.Background(), mustJSON(t, `{"command":["echo","x"]}`))
	if err == nil {
		t.Error("expected error for nil runner")
	}
}

func TestCtxExecute_Execute_NonZeroExit(t *testing.T) {
	cmd, ok := nonZeroExitCmd()
	if !ok {
		t.Skip("no non-zero equivalent")
	}
	tool := NewCtxExecuteTool(newCtxTestRunner(t), ".")
	res, _ := tool.Execute(context.Background(), mustJSON(t, jsonObj(t, map[string]any{
		"command": cmd,
	})))
	if res.Err == nil {
		t.Error("expected Err for non-zero exit")
	}
	if !strings.HasPrefix(res.Err.Error(), "command_failed exit=1") {
		t.Errorf("err = %q, want 'command_failed exit=1' prefix", res.Err.Error())
	}
	// The text should still contain the JSON result.
	var pr ctxexec.Result
	if err := json.Unmarshal([]byte(res.Text), &pr); err != nil {
		t.Errorf("text not json: %v", err)
	}
	if pr.ExitCode == 0 {
		t.Error("exit should be non-zero in json")
	}
}

func TestCtxExecute_Execute_FailureIncludesStderrTail(t *testing.T) {
	cmd, ok := stderrFailCmd()
	if !ok {
		t.Skip("no stderr-fail equivalent")
	}
	tool := NewCtxExecuteTool(newCtxTestRunner(t), ".")
	res, _ := tool.Execute(context.Background(), mustJSON(t, jsonObj(t, map[string]any{
		"command": cmd,
	})))
	if res.Err == nil {
		t.Fatal("expected Err for failing command")
	}
	msg := res.Err.Error()
	if !strings.HasPrefix(msg, "command_failed exit=3") {
		t.Errorf("err = %q, want 'command_failed exit=3' prefix", msg)
	}
	if !strings.Contains(msg, "stderr:") || !strings.Contains(msg, "oops") {
		t.Errorf("err = %q, want stderr tail with 'oops'", msg)
	}
}

func TestCtxExecute_Execute_MissingInterpreter(t *testing.T) {
	tool := NewCtxExecuteTool(newCtxTestRunner(t), ".")
	res, _ := tool.Execute(context.Background(), mustJSON(t, `{"command":["definitely_not_a_binary_xyz_42"]}`))
	if res.Err == nil {
		t.Error("expected Err for missing interpreter")
	}
	if !strings.Contains(res.Err.Error(), "not found") {
		t.Errorf("err = %q, want 'not found'", res.Err.Error())
	}
}

func TestCtxExecute_Execute_TimeoutFlag(t *testing.T) {
	cmd, ok := sleepCmd(5)
	if !ok {
		t.Skip("no sleep equivalent")
	}
	tool := NewCtxExecuteTool(newCtxTestRunner(t), ".")
	res, _ := tool.Execute(context.Background(), mustJSON(t, jsonObj(t, map[string]any{
		"command":    cmd,
		"timeout_ms": 300,
	})))
	if res.Err == nil {
		t.Error("expected Err for timeout")
	}
	if !strings.HasPrefix(res.Err.Error(), "command_failed timeout") {
		t.Errorf("err = %q, want 'command_failed timeout' prefix", res.Err.Error())
	}
	// Underlying Result should have exit_code == ExitTimeout (124).
	var pr ctxexec.Result
	_ = json.Unmarshal([]byte(res.Text), &pr)
	if pr.ExitCode != ctxexec.ExitTimeout {
		t.Errorf("exit = %d, want %d", pr.ExitCode, ctxexec.ExitTimeout)
	}
	if pr.DurationMS > 2500 {
		t.Errorf("duration = %d, expected near 300", pr.DurationMS)
	}
}

func TestCtxExecute_Execute_TruncationFlag(t *testing.T) {
	cmd, ok := stdoutFloodCmd()
	if !ok {
		t.Skip("no flood equivalent")
	}
	tool := NewCtxExecuteTool(newCtxTestRunner(t), ".")
	res, _ := tool.Execute(context.Background(), mustJSON(t, jsonObj(t, map[string]any{
		"command":       cmd,
		"timeout_ms":    800,
		"max_stdout_kb": 1,
		"max_stderr_kb": 1,
	})))
	var pr ctxexec.Result
	_ = json.Unmarshal([]byte(res.Text), &pr)
	if !pr.TruncatedStdout {
		t.Errorf("TruncatedStdout = false, want true; result=%+v", pr)
	}
}

func TestCtxExecute_Execute_WorkdirEscape(t *testing.T) {
	cmd, ok := echoCmd("hi")
	if !ok {
		t.Skip("no echo equivalent")
	}
	tool := NewCtxExecuteTool(newCtxTestRunner(t), ".")
	res, _ := tool.Execute(context.Background(), mustJSON(t, jsonObj(t, map[string]any{
		"command": cmd,
		"workdir": "../../../etc",
	})))
	if res.Err == nil {
		t.Error("expected Err for escape")
	}
}

func TestCtxExecute_FormatForTUI(t *testing.T) {
	out := formatCtxExecForTUI(&ctxexec.Result{
		ExitCode: 0, DurationMS: 12,
		Stdout: "hello\n", Stderr: "warn\n", Workdir: "/h",
	})
	for _, want := range []string{"exit: 0", "duration: 12ms",
		"stdout:", "hello", "stderr:", "warn", "workdir: /h"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestCtxExecute_FormatForTUI_Nil(t *testing.T) {
	if got := formatCtxExecForTUI(nil); got != "(nil result)" {
		t.Errorf("got %q", got)
	}
}

func TestCtxExecute_FormatForTUI_Error(t *testing.T) {
	out := formatCtxExecForTUI(&ctxexec.Result{
		ExitCode: 127, Error: "interpreter not found",
	})
	if !strings.Contains(out, "interpreter not found") {
		t.Errorf("got %q", out)
	}
}

func TestCtxExecute_Execute_RunnerRejectsHugeCommand(t *testing.T) {
	tool := NewCtxExecuteTool(newCtxTestRunner(t), ".")
	// 5 KB arg
	huge := strings.Repeat("x", 5000)
	res, _ := tool.Execute(context.Background(), mustJSON(t, jsonObj(t, map[string]any{
		"command": []string{"echo", huge},
	})))
	if res.Err == nil {
		t.Error("expected Err for oversized command")
	}
}

func TestCtxExecute_Execute_RunnerRejectsEscape(t *testing.T) {
	cmd, ok := echoCmd("hi")
	if !ok {
		t.Skip("no echo equivalent")
	}
	tool := NewCtxExecuteTool(newCtxTestRunner(t), ".")
	res, _ := tool.Execute(context.Background(), mustJSON(t, jsonObj(t, map[string]any{
		"command": cmd,
		"workdir": "/etc",
	})))
	if res.Err == nil {
		t.Error("expected Err for absolute escape")
	}
}

// platform helpers (mirrored from ctxexec tests) -------

func echoCmd(text string) ([]string, bool) {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("cmd"); err == nil {
			return []string{"cmd", "/c", "echo", text}, true
		}
		return nil, false
	}
	if _, err := exec.LookPath("echo"); err == nil {
		return []string{"echo", text}, true
	}
	return nil, false
}

func sleepCmd(seconds int) ([]string, bool) {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("ping"); err == nil {
			return []string{"ping", "-n", itoa(seconds + 1), "127.0.0.1"}, true
		}
		return nil, false
	}
	if _, err := exec.LookPath("sleep"); err == nil {
		return []string{"sleep", itoa(seconds)}, true
	}
	return nil, false
}

// stderrFailCmd prints "oops" to stderr and exits 3.
func stderrFailCmd() ([]string, bool) {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("cmd"); err == nil {
			return []string{"cmd", "/c", "echo oops 1>&2 & exit 3"}, true
		}
		return nil, false
	}
	if _, err := exec.LookPath("sh"); err == nil {
		return []string{"sh", "-c", "echo oops >&2; exit 3"}, true
	}
	return nil, false
}

func nonZeroExitCmd() ([]string, bool) {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("cmd"); err == nil {
			return []string{"cmd", "/c", "exit 1"}, true
		}
		return nil, false
	}
	if _, err := exec.LookPath("false"); err == nil {
		return []string{"false"}, true
	}
	return nil, false
}

func stdoutFloodCmd() ([]string, bool) {
	for _, name := range []string{"python3", "python", "py"} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if _, err := exec.Command(p, "--version").Output(); err != nil {
			if name == "py" {
				return []string{p, "-3", "-u", "-c",
					"import sys, time; " +
						"[sys.stdout.write('a' * 65536) or sys.stdout.flush() or time.sleep(0.001) for _ in range(10000)]"}, true
			}
			continue
		}
		return []string{p, "-u", "-c",
			"import sys, time; " +
				"[sys.stdout.write('a' * 65536) or sys.stdout.flush() or time.sleep(0.001) for _ in range(10000)]"}, true
	}
	return nil, false
}

func jsonObj(t *testing.T, m map[string]any) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := "0123456789"
	buf := make([]byte, 0, 12)
	for n > 0 {
		buf = append([]byte{digits[n%10]}, buf...)
		n /= 10
	}
	return string(buf)
}
