package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/agent"
	"supercli/internal/tools/shellescape"
)

func TestShellCancelBeforeResultKeepsForegroundAndDraft(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyCtrlC, tea.KeyEsc} {
		t.Run(tea.KeyMsg{Type: key}.String(), func(t *testing.T) {
			m := New(Options{NoColor: true, ShellRunner: shellescape.NewRunner(t.TempDir())})
			next, cmd := m.dispatchShellEscape("!echo synthetic")
			m = next.(Model)
			invocation := m.shellInvocation
			if cmd == nil || invocation == nil || !m.busy || !m.cancel.IsArmed() {
				t.Fatal("accepted shell must own foreground and cancellation")
			}
			t.Cleanup(invocation.cancel)
			if _, ok := invocation.ctx.Deadline(); ok {
				t.Fatal("shell dispatch added a runtime deadline")
			}
			m.input.SetValue("unsent next prompt")
			m.pendingAttachments = []string{"synthetic-attachment"}
			next, _ = m.Update(tea.KeyMsg{Type: key})
			m = next.(Model)
			if !errors.Is(invocation.ctx.Err(), context.Canceled) || !m.busy || !m.cancelling || m.shellInvocation != invocation || m.quitting {
				t.Fatal("Stop must cancel the shell context and retain foreground until its result")
			}
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(Model)
			if m.input.Value() != "unsent next prompt" || !m.busy || m.shellInvocation != invocation || m.submittingDraft != "" {
				t.Fatal("Enter while shell cancellation drains must not submit or consume the draft")
			}
			result := &shellescape.Result{Command: "synthetic", ExitCode: 1, Stdout: "before-stop stdout", Stderr: "before-stop stderr"}
			original := *result
			next, _ = m.Update(shellResultMsg{res: result, invocation: invocation})
			m = next.(Model)
			if m.busy || m.cancelling || m.cancel.IsArmed() || m.shellInvocation != nil {
				t.Fatal("matching terminal result did not release shell foreground")
			}
			if !strings.Contains(m.completedLines(), result.Stdout) || !strings.Contains(m.completedLines(), result.Stderr) {
				t.Fatal("cancellation discarded the retained command output")
			}
			if *result != original || m.input.Value() != "unsent next prompt" || !reflect.DeepEqual(m.pendingAttachments, []string{"synthetic-attachment"}) {
				t.Fatal("shell completion changed the canonical result or unsent draft")
			}
		})
	}
}

type shellLifecycleAgent struct {
	ctx context.Context
}

func (a *shellLifecycleAgent) Name() string { return "shell-lifecycle-fixture" }
func (a *shellLifecycleAgent) Run(ctx context.Context, _ string) (<-chan agent.Event, error) {
	a.ctx = ctx
	ch := make(chan agent.Event)
	close(ch)
	return ch, nil
}

func TestStaleShellResultPreservesNewInvocation(t *testing.T) {
	for _, kind := range []string{"agent", "shell"} {
		t.Run(kind, func(t *testing.T) {
			ag := &shellLifecycleAgent{}
			m := New(Options{NoColor: true, Agent: ag, ShellRunner: shellescape.NewRunner(t.TempDir())})
			next, _ := m.dispatchShellEscape("!echo old")
			m = next.(Model)
			old := m.shellInvocation
			t.Cleanup(old.cancel)
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			m = next.(Model)
			next, _ = m.Update(shellResultMsg{res: &shellescape.Result{Command: "old", ExitCode: 1}, invocation: old})
			m = next.(Model)
			var current context.Context
			var currentShell *shellInvocation
			if kind == "shell" {
				next, _ = m.dispatchShellEscape("!echo new")
				m = next.(Model)
				currentShell = m.shellInvocation
				current = currentShell.ctx
				t.Cleanup(currentShell.cancel)
			} else {
				var cmd tea.Cmd
				next, cmd = m.startPrompt("new agent prompt")
				m = next.(Model)
				if cmd == nil {
					t.Fatal("new agent invocation was not accepted")
				}
				next, _ = m.Update(cmd())
				m = next.(Model)
				current = ag.ctx
				t.Cleanup(func() { m.cancel.Cancel() })
			}
			m.input.SetValue("keep newer draft")
			m.input.Blur()
			m.pendingAttachments = []string{"newer-attachment"}
			m.chat.addSystem(m.marker.Running())
			beforeRunning := strings.Count(m.completedLines(), m.marker.Running())
			next, _ = m.Update(shellResultMsg{res: &shellescape.Result{Command: "old", ExitCode: 1, Stdout: "late retained output"}, invocation: old})
			m = next.(Model)
			if !m.busy || m.cancelling || !m.cancel.IsArmed() || current.Err() != nil || m.shellInvocation != currentShell {
				t.Fatal("stale result changed newer foreground or cancellation ownership")
			}
			if m.input.Value() != "keep newer draft" || m.input.Focused() || !reflect.DeepEqual(m.pendingAttachments, []string{"newer-attachment"}) {
				t.Fatal("stale result changed the newer composer")
			}
			if !strings.Contains(m.completedLines(), "late retained output") || strings.Count(m.completedLines(), m.marker.Running()) != beforeRunning {
				t.Fatal("stale result lost its output or removed the newer running marker")
			}
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			m = next.(Model)
			if !errors.Is(current.Err(), context.Canceled) || !m.busy || !m.cancelling {
				t.Fatal("newer invocation no longer responds to Stop")
			}
		})
	}
}

// The helper uses a readiness/release socket instead of sleeps or process
// polling. It emits output before readiness, then remains alive until released
// by the success/failure test or killed through the actual TUI Cancel context.
func TestTUIShellLifecycleHelper(t *testing.T) {
	args := os.Args
	if len(args) < 4 || args[len(args)-3] != "--tui-shell-lifecycle-helper" {
		return
	}
	mode, address := args[len(args)-2], args[len(args)-1]
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintln(os.Stdout, "synthetic stdout Żółć")
	fmt.Fprintln(os.Stderr, "synthetic stderr Żółć")
	if _, err := conn.Write([]byte("R")); err != nil {
		t.Fatal(err)
	}
	var release [1]byte
	if _, err := io.ReadFull(conn, release[:]); err != nil {
		os.Exit(0)
	}
	if mode == "failure" {
		os.Exit(7)
	}
	os.Exit(0)
}

func shellLifecycleCommand(t *testing.T, mode, address string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := " -test.run=^TestTUIShellLifecycleHelper$ -- --tui-shell-lifecycle-helper " + mode + " " + address
	if runtime.GOOS == "windows" {
		return `""` + executable + `"` + args + `"`
	}
	return "'" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "'" + args
}

func shellLifecycleLaunch(t *testing.T, mode string) (Model, net.Conn, <-chan tea.Msg) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	m := New(Options{NoColor: true, ShellRunner: shellescape.NewRunner(t.TempDir())})
	next, cmd := m.dispatchShellEscape("!" + shellLifecycleCommand(t, mode, listener.Addr().String()))
	m = next.(Model)
	invocation := m.shellInvocation
	if cmd == nil || invocation == nil || !m.cancel.IsArmed() {
		t.Fatal("shell invocation was not accepted")
	}
	t.Cleanup(invocation.cancel)
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ready [1]byte
	if _, err := io.ReadFull(conn, ready[:]); err != nil || ready[0] != 'R' {
		t.Fatalf("helper readiness: %q, %v", ready, err)
	}
	return m, conn, result
}

func shellLifecycleAwait(t *testing.T, result <-chan tea.Msg) shellResultMsg {
	t.Helper()
	select {
	case msg := <-result:
		if shell, ok := msg.(shellResultMsg); ok {
			return shell
		}
		t.Fatalf("unexpected shell terminal message %T", msg)
	case <-time.After(10 * time.Second):
		t.Fatal("shell invocation did not terminate")
	}
	return shellResultMsg{}
}

func TestShellInputCancellationReachesActualRunner(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyCtrlC, tea.KeyEsc} {
		t.Run(tea.KeyMsg{Type: key}.String(), func(t *testing.T) {
			m, conn, result := shellLifecycleLaunch(t, "wait")
			invocation := m.shellInvocation
			m.input.SetValue("draft during native command")
			next, _ := m.Update(tea.KeyMsg{Type: key})
			m = next.(Model)
			if !m.busy || !m.cancelling || !errors.Is(invocation.ctx.Err(), context.Canceled) {
				t.Fatal("input Stop did not reach live shell invocation")
			}
			msg := shellLifecycleAwait(t, result)
			if msg.invocation != invocation || msg.res.ExitCode == 0 {
				t.Fatal("cancelled native command returned success or a different owner")
			}
			var b [1]byte
			if n, err := conn.Read(b[:]); n != 0 || err == nil {
				t.Fatalf("cancelled child still holds readiness socket: n=%d, err=%v", n, err)
			} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatalf("cancelled child did not close readiness socket: %v", err)
			}
			next, _ = m.Update(msg)
			m = next.(Model)
			if m.busy || m.cancelling || m.shellInvocation != nil || m.input.Value() != "draft during native command" {
				t.Fatal("native shell completion lost draft or did not release foreground")
			}
		})
	}
}

func TestShellNormalCompletionPreservesResult(t *testing.T) {
	for _, mode := range []string{"success", "failure"} {
		t.Run(mode, func(t *testing.T) {
			m, conn, result := shellLifecycleLaunch(t, mode)
			m.input.SetValue("keep completion draft")
			if _, err := conn.Write([]byte("C")); err != nil {
				t.Fatal(err)
			}
			msg := shellLifecycleAwait(t, result)
			wantExit := 0
			if mode == "failure" {
				wantExit = 7
			}
			if msg.res.ExitCode != wantExit || msg.res.Error != "" || !strings.Contains(msg.res.Stdout, "synthetic stdout Żółć") || !strings.Contains(msg.res.Stderr, "synthetic stderr Żółć") {
				t.Fatalf("native result changed: %+v", msg.res)
			}
			// The generated helper executable path can exhaust the existing 200-byte
			// command preview before stdout. Use a short fixture label for this
			// presentation assertion; the real Runner result was checked above.
			msg.res.Command = "fixture-child"
			original := *msg.res
			next, _ := m.Update(msg)
			m = next.(Model)
			if *msg.res != original || m.busy || m.cancelling || m.cancel.IsArmed() || m.shellInvocation != nil || m.input.Value() != "keep completion draft" {
				t.Fatal("normal shell result changed canonical output, lifecycle or draft")
			}
			view := m.completedLines()
			if !strings.Contains(view, "synthetic stdout Żółć") || mode == "failure" && !strings.Contains(view, "synthetic stderr Żółć") {
				t.Fatal("normal shell rendering lost retained output")
			}
		})
	}
}

func TestShellCompletionPreservesRunnerError(t *testing.T) {
	m := New(Options{NoColor: true, ShellRunner: shellescape.NewRunner(t.TempDir())})
	next, _ := m.dispatchShellEscape("!echo synthetic")
	m = next.(Model)
	result := &shellescape.Result{Command: "synthetic", ExitCode: -1, Error: "synthetic launch failure", Stdout: "canonical stdout", Stderr: "canonical stderr"}
	original := *result
	next, _ = m.Update(shellResultMsg{res: result, invocation: m.shellInvocation})
	m = next.(Model)
	if *result != original || !strings.Contains(m.completedLines(), result.Error) || m.busy || m.cancelling || m.cancel.IsArmed() || m.shellInvocation != nil {
		t.Fatal("runner error completion changed error or failed to release foreground")
	}
}
