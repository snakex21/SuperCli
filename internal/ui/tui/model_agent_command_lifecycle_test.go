package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/agent"
	"supercli/internal/checkpoint"
	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/ctxexec"
)

// Exercise the real agent, checkpoint wrapper and native runner through TUI
// submit/key/event handling. Only the provider is scripted; no network model
// call or user conversation is involved.
type tuiCommandProvider struct {
	command []string
	calls   int // Owned by the Loop, read after its event channel closes.
}

func (*tuiCommandProvider) Name() string { return "tui-command-lifecycle" }
func (p *tuiCommandProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.calls++
	out := make(chan llm.Delta, 2)
	if p.calls == 1 {
		args, err := json.Marshal(map[string]any{"command": p.command})
		if err != nil {
			return nil, err
		}
		out <- llm.Delta{ToolCall: &llm.ToolCall{ID: "native-command", Name: "ctx_execute", Arguments: string(args)}}
		out <- llm.Delta{FinishReason: "tool_calls"}
	} else {
		out <- llm.Delta{Content: "TUI response after command", FinishReason: "stop"}
	}
	close(out)
	return out, nil
}

func tuiNativeCommand(text string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/d", "/c", text}
	}
	return []string{"sh", "-c", text}
}

func tuiCommandModel(t *testing.T, home string, command []string) (Model, *tuiCommandProvider, <-chan error) {
	t.Helper()
	manager, err := checkpoint.Open(home, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	controller := checkpoint.NewController(manager, "tui-native-command")
	registry := tools.NewRegistry()
	registry.MustRegister(controller.Wrap(tools.NewCtxExecuteTool(ctxexec.New(home), home).Spec()))
	registry.MarkAlwaysOn("ctx_execute")
	provider := &tuiCommandProvider{command: command}
	loop, err := agent.NewLoop(agent.LoopConfig{Provider: provider, Registry: registry, BaseDir: home, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 2)
	m := New(Options{NoColor: true, Agent: loop, LLM: provider,
		OnRunStart: func() { controller.Start("TUI native command") },
		OnRunEnd: func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := controller.CompleteDeferred(ctx, func(_ *checkpoint.Record, err error) { completed <- err })
			completed <- err
		},
	})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	return next.(Model), provider, completed
}

func tuiCommandSubmit(t *testing.T, m Model, prompt string) Model {
	t.Helper()
	m.input.SetValue(prompt)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil || !m.busy {
		t.Fatal("TUI did not accept foreground prompt")
	}
	started := make(chan tea.Msg, 1)
	go func() { started <- cmd() }()
	select {
	case msg := <-started:
		next, _ = m.Update(msg)
		m = next.(Model)
	case <-time.After(10 * time.Second):
		t.Fatal("TUI prompt admission did not finish")
	}
	if m.eventCh == nil {
		t.Fatal("TUI prompt did not attach the agent event channel")
	}
	t.Cleanup(func() { m.cancel.Cancel() })
	return m
}

// Each read blocks on the existing completion/event channel. No status/file
// polling or guessed process delays are used.
func tuiCommandDrain(t *testing.T, m Model) (Model, []agent.ToolResultEvent, bool) {
	t.Helper()
	deadline := time.NewTimer(12 * time.Second)
	defer deadline.Stop()
	var results []agent.ToolResultEvent
	var canceled bool
	for {
		select {
		case ev, ok := <-m.eventCh:
			if !ok {
				next, _ := m.Update(runEndMsg{})
				m = next.(Model)
				if m.busy || m.cancelling || m.eventCh != nil || m.cancel.IsArmed() {
					t.Fatal("closed command run retained the TUI foreground")
				}
				return m, results, canceled
			}
			if result, ok := ev.(agent.ToolResultEvent); ok {
				results = append(results, result)
			}
			if failure, ok := ev.(agent.ErrorEvent); ok && errors.Is(failure.Err, context.Canceled) {
				canceled = true
			}
			next, _ := m.Update(runEventMsg{ev: ev})
			m = next.(Model)
		case <-deadline.C:
			t.Fatal("command/Stop did not close the real agent run")
		}
	}
}

func tuiCommandCheckpointDone(t *testing.T, completed <-chan error) {
	t.Helper()
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal("command checkpoint completion:", err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("command checkpoint owner did not finish")
	}
}

func TestTUIAgentNativeCommandCompletionAndNextPrompt(t *testing.T) {
	for _, mode := range []string{"success", "failure", "checkpoint-limit"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			command := tuiNativeCommand("echo TUI_NATIVE_COMPLETED")
			if mode == "failure" {
				command = tuiNativeCommand("exit 7")
			}
			if mode == "checkpoint-limit" {
				file, err := os.Create(filepath.Join(home, "oversized.bin"))
				if err != nil {
					t.Fatal(err)
				}
				err = file.Truncate(257 << 20)
				closeErr := file.Close()
				if err != nil || closeErr != nil {
					t.Fatal(errors.Join(err, closeErr))
				}
			}
			m, provider, completed := tuiCommandModel(t, home, command)
			m = tuiCommandSubmit(t, m, "run the native command")
			m.input.SetValue("draft survives native completion")
			m, results, canceled := tuiCommandDrain(t, m)
			if canceled || len(results) != 1 || results[0].ID != "native-command" {
				t.Fatalf("native result pairing: canceled=%t results=%+v", canceled, results)
			}
			result := results[0]
			if mode == "checkpoint-limit" {
				if !errors.Is(result.Err, checkpoint.ErrSnapshotLimit) {
					t.Fatalf("checkpoint rejection never reached TUI: %+v", result)
				}
			} else {
				var native ctxexec.Result
				if err := json.Unmarshal([]byte(result.Output), &native); err != nil {
					t.Fatal(err)
				}
				wantExit := 0
				if mode == "failure" {
					wantExit = 7
				}
				if native.ExitCode != wantExit || (mode == "success" && !strings.Contains(native.Stdout, "TUI_NATIVE_COMPLETED")) {
					t.Fatalf("native output lost: %+v", native)
				}
			}
			if m.input.Value() != "draft survives native completion" {
				t.Fatal("command completion discarded the unsent draft")
			}
			tuiCommandCheckpointDone(t, completed)
			m = tuiCommandSubmit(t, m, "next prompt after command")
			_, nextResults, _ := tuiCommandDrain(t, m)
			tuiCommandCheckpointDone(t, completed)
			if provider.calls != 3 || len(nextResults) != 0 {
				t.Fatal("next TUI prompt was blocked or reran the command")
			}
		})
	}
}

func TestTUIAgentStopNativeCommandAndNextPrompt(t *testing.T) {
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("native curl is unavailable")
	}
	for _, key := range []tea.KeyType{tea.KeyCtrlC, tea.KeyEsc} {
		t.Run(tea.KeyMsg{Type: key}.String(), func(t *testing.T) {
			entered, closed := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintln(w, "native curl is ready")
				w.(http.Flusher).Flush()
				close(entered)
				<-r.Context().Done()
				close(closed)
			}))
			defer server.Close()
			m, provider, completed := tuiCommandModel(t, t.TempDir(), []string{curl, "--silent", "--no-buffer", server.URL})
			m = tuiCommandSubmit(t, m, "run command until Stop")
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("native curl never reached the owned server")
			}
			m.input.SetValue("draft survives Stop")
			next, _ := m.Update(tea.KeyMsg{Type: key})
			m = next.(Model)
			m, results, canceled := tuiCommandDrain(t, m)
			if !canceled || len(results) != 1 || !errors.Is(results[0].Err, context.Canceled) {
				t.Fatalf("Stop did not complete the command pair: canceled=%t results=%+v", canceled, results)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("Stop left the native curl connection alive")
			}
			if m.input.Value() != "draft survives Stop" {
				t.Fatal("Stop discarded the unsent draft")
			}
			if !strings.Contains(results[0].Output, "native curl is ready") {
				t.Fatal("Stop discarded accepted native output")
			}
			tuiCommandCheckpointDone(t, completed)
			m = tuiCommandSubmit(t, m, "next prompt after Stop")
			_, nextResults, _ := tuiCommandDrain(t, m)
			tuiCommandCheckpointDone(t, completed)
			if provider.calls != 2 || len(nextResults) != 0 {
				t.Fatal("next TUI prompt was blocked or restarted curl")
			}
		})
	}
}
