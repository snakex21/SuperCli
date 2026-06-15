package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage"
	"supercli/internal/storage/goal"
	"supercli/internal/system/stats"
)

type testModelLister struct{ models []llm.ModelInfo }

func (t testModelLister) ListModels() []llm.ModelInfo { return t.models }

type testModelSwapper struct{ current string }

func (t *testModelSwapper) CurrentModel() string    { return t.current }
func (t *testModelSwapper) SetModel(p llm.Provider) { t.current = p.Name() }

// newMockStatsRecorder returns a stats.Memory for tests.
func newMockStatsRecorder() stats.Recorder {
	return stats.NewMemory()
}

func TestNew_StoresContext(t *testing.T) {
	m := New(Options{Home: "/x", DataDir: "/x/.supercli"})
	if m.home != "/x" || m.dataDir != "/x/.supercli" {
		t.Fatalf("home/dataDir = %q,%q", m.home, m.dataDir)
	}
	if m.busy {
		t.Fatal("busy should default to false")
	}
	if m.width != 0 || m.height != 0 {
		t.Fatal("width/height should default to 0")
	}
}

func TestNew_EmptyContextRendersPlaceholder(t *testing.T) {
	m := New(Options{})
	view := m.View()
	// The view should contain the separator line (───) and the input prompt.
	if !strings.Contains(view, "─") {
		t.Fatalf("expected separator line, got %q", view)
	}
	if !strings.Contains(view, "❯") {
		t.Fatalf("expected input prompt, got %q", view)
	}
}

func TestUpdate_QIsNormalInput(t *testing.T) {
	m := New(Options{Home: "/x"})
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	mm := out.(Model)
	if mm.quitting {
		t.Fatal("q must not quit; it is normal input")
	}
	if mm.input.Value() != "q" {
		t.Fatalf("input = %q, want q", mm.input.Value())
	}
}

func TestUpdate_QuitOnCtrlC(t *testing.T) {
	m := New(Options{Home: "/x"})
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	mm := out.(Model)
	if !mm.quitting {
		t.Fatal("expected quitting=true on ctrl+c")
	}
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd")
	}
}

func TestUpdate_EscClearsInputDoesNotQuit(t *testing.T) {
	m := New(Options{Home: "/x"})
	m.input.SetValue("draft")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm := out.(Model)
	if mm.quitting {
		t.Fatal("esc must not quit")
	}
	if mm.input.Value() != "" {
		t.Fatalf("esc should clear input, got %q", mm.input.Value())
	}
}

func TestUpdate_QuitOnSlashQuit(t *testing.T) {
	m := New(Options{Home: "/x"})
	m.input.SetValue("/quit")
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if !mm.quitting {
		t.Fatal("expected /quit to set quitting=true")
	}
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd")
	}
}

func TestUpdate_DarwinSlashDoesNotQuitOrCrash(t *testing.T) {
	m := New(Options{
		Home: "/x",
		Commands: map[string]SlashHandler{
			"darwin": func(_ context.Context, args string) (string, error) {
				if strings.TrimSpace(args) == "" {
					return "usage: /darwin [N] <prompt>", nil
				}
				return "darwin ok", nil
			},
		},
	})
	m.input.SetValue("/darwin")
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if mm.quitting {
		t.Fatal("/darwin must not quit the TUI")
	}
	if !mm.busy {
		t.Fatal("/darwin should run as a slash command")
	}
	if cmd == nil {
		t.Fatal("expected slash command cmd")
	}
	msg := cmd()
	res, ok := msg.(slashResultMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want slashResultMsg", msg)
	}
	if res.Err != nil {
		t.Fatalf("/darwin returned error: %v", res.Err)
	}
	if !strings.Contains(res.Body, "usage: /darwin") {
		t.Fatalf("expected safe usage output, got %q", res.Body)
	}
}

func TestUpdate_SlashCommandPanicIsRecovered(t *testing.T) {
	m := New(Options{
		Home: "/x",
		Commands: map[string]SlashHandler{
			"darwin": func(_ context.Context, _ string) (string, error) {
				panic("boom")
			},
		},
	})
	m.input.SetValue("/darwin 3 test")
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if mm.quitting {
		t.Fatal("panic in slash command must not quit the TUI")
	}
	res := cmd().(slashResultMsg)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "panic") {
		t.Fatalf("expected recovered panic error, got %+v", res)
	}
}

func TestUpdate_NilSlashHandlerIsRecovered(t *testing.T) {
	m := New(Options{
		Home: "/x",
		Commands: map[string]SlashHandler{
			"darwin": nil,
		},
	})
	m.input.SetValue("/darwin 3 test")
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if mm.quitting {
		t.Fatal("nil slash handler must not quit the TUI")
	}
	if cmd == nil {
		t.Fatal("expected slash command cmd")
	}
	res := cmd().(slashResultMsg)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "not wired") {
		t.Fatalf("expected nil handler error, got %+v", res)
	}
}

func TestUpdate_SlashResultErrorDoesNotQuit(t *testing.T) {
	m := New(Options{Home: "/x"})
	out, _ := m.Update(slashResultMsg{Err: errors.New("darwin failed")})
	mm := out.(Model)
	if mm.quitting {
		t.Fatal("slashResultMsg error must not quit")
	}
	if mm.busy {
		t.Fatal("slashResultMsg should clear busy")
	}
	if !strings.Contains(mm.transcript.String(), "darwin failed") {
		t.Fatalf("expected error in transcript, got %q", mm.transcript.String())
	}
}

func TestUpdate_AllCoreSlashCommandsAreSimulated(t *testing.T) {
	commands := []string{
		"help", "darwin", "council", "clear", "reflect",
		"compact", "status", "sandbox",
	}
	for _, name := range commands {
		t.Run(name, func(t *testing.T) {
			m := New(Options{
				Home: "/x",
				Commands: map[string]SlashHandler{
					name: func(_ context.Context, args string) (string, error) {
						return "ok:" + args, nil
					},
				},
			})
			m.input.SetValue("/" + name + " sample args")
			out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			mm := out.(Model)
			if mm.quitting {
				t.Fatalf("/%s unexpectedly quit after enter", name)
			}
			if localSlashCommands[name] {
				// Local commands must NOT flip the TUI into the
				// busy/running state (no spinner, no abort hint).
				if mm.busy {
					t.Fatalf("/%s is local and must not set busy", name)
				}
			} else if !mm.busy {
				t.Fatalf("/%s should set busy while handler runs", name)
			}
			if cmd == nil {
				t.Fatalf("/%s should return a command", name)
			}
			msg := cmd()
			res, ok := msg.(slashResultMsg)
			if !ok {
				t.Fatalf("/%s cmd() = %T, want slashResultMsg", name, msg)
			}
			if res.Err != nil {
				t.Fatalf("/%s returned error: %v", name, res.Err)
			}
			out, cmd = mm.Update(res)
			mm = out.(Model)
			if mm.quitting {
				t.Fatalf("/%s unexpectedly quit after result", name)
			}
			if mm.busy {
				t.Fatalf("/%s should clear busy after result", name)
			}
			if cmd != nil {
				t.Fatalf("/%s result should not schedule another command", name)
			}
			if !strings.Contains(mm.transcript.String(), "ok:sample args") {
				t.Fatalf("/%s transcript missing result: %q", name, mm.transcript.String())
			}
		})
	}
}

func TestUpdate_AllCoreSlashCommandErrorsAreNonFatal(t *testing.T) {
	commands := []string{
		"help", "darwin", "council", "clear", "reflect",
		"compact", "status", "sandbox",
	}
	for _, name := range commands {
		t.Run(name, func(t *testing.T) {
			m := New(Options{
				Home: "/x",
				Commands: map[string]SlashHandler{
					name: func(_ context.Context, _ string) (string, error) {
						return "", errors.New("boom " + name)
					},
				},
			})
			m.input.SetValue("/" + name)
			out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			mm := out.(Model)
			if mm.quitting {
				t.Fatalf("/%s unexpectedly quit after enter", name)
			}
			res := cmd().(slashResultMsg)
			out, _ = mm.Update(res)
			mm = out.(Model)
			if mm.quitting {
				t.Fatalf("/%s error unexpectedly quit", name)
			}
			if mm.busy {
				t.Fatalf("/%s error should clear busy", name)
			}
			if !strings.Contains(mm.transcript.String(), "boom "+name) {
				t.Fatalf("/%s transcript missing error: %q", name, mm.transcript.String())
			}
		})
	}
}

func TestUpdate_UnknownSlashCommandIsNonFatal(t *testing.T) {
	m := New(Options{Home: "/x", Commands: map[string]SlashHandler{}})
	m.input.SetValue("/doesnotexist")
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if mm.quitting {
		t.Fatal("unknown slash command must not quit")
	}
	if mm.busy {
		t.Fatal("unknown slash command must not set busy")
	}
	if cmd != nil {
		t.Fatal("unknown slash command must not schedule a command")
	}
	if !strings.Contains(mm.transcript.String(), "unknown command") {
		t.Fatalf("expected unknown command in transcript, got %q", mm.transcript.String())
	}
}

func TestInteractiveModelsMenu_FilterAndSelect(t *testing.T) {
	swapper := &testModelSwapper{current: "old"}
	m := New(Options{
		Home:         "/x",
		ModelSwapper: swapper,
		ModelLister: testModelLister{models: []llm.ModelInfo{
			{ID: "gpt-4o", Provider: "openai", ContextLength: 128000, InputCost: 2.5, OutputCost: 10, Vision: true, ToolUse: true},
			{ID: "claude-sonnet", Provider: "anthropic", ContextLength: 200000, Reasoning: true, ToolUse: true},
		}},
		ModelSwapFn: func(modelID, provider string) (llm.Provider, error) { return stubLLM{n: modelID}, nil },
	})
	m.input.SetValue("/model")
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if cmd != nil {
		t.Fatal("/models menu should not run async command")
	}
	if mm.mode != modeMenu || mm.menu.kind != menuModels {
		t.Fatalf("not in models menu: mode=%v kind=%v", mm.mode, mm.menu.kind)
	}
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	mm = out.(Model)
	if !strings.Contains(mm.renderMenuView(), "claude-sonnet") {
		t.Fatalf("filtered view missing sonnet: %q", mm.renderMenuView())
	}
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = out.(Model)
	// Confirming the picker applies the swap synchronously (rebuild
	// provider + SetModel) right away — it no longer defers the work
	// to an async modelSwapRequestMsg that only runs later.
	if mm.mode != modeNormal {
		t.Fatalf("picker should close on confirm, mode=%v", mm.mode)
	}
	if swapper.current != "claude-sonnet" {
		t.Fatalf("model not swapped on confirm: current=%q, want claude-sonnet", swapper.current)
	}
}

func TestInteractiveProvidersMenu_OpensAndBacksOut(t *testing.T) {
	m := New(Options{Home: "/x"})
	m.input.SetValue("/providers")
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if cmd != nil {
		t.Fatal("/providers menu should not run async command")
	}
	if mm.mode != modeMenu || mm.menu.kind != menuProviders {
		t.Fatalf("not provider menu: mode=%v kind=%v", mm.mode, mm.menu.kind)
	}
	if !strings.Contains(mm.View(), "Providers") {
		t.Fatalf("provider menu title missing: %q", mm.View())
	}
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm = out.(Model)
	if mm.mode != modeNormal {
		t.Fatalf("esc should return to normal mode, got %v", mm.mode)
	}
}

func TestInteractiveGoalMenu_TogglesTask(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	gs := goal.NewStorage(db)
	if err := gs.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc := goal.NewService(gs)
	if _, err := svc.Set(context.Background(), "ship menus", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddTask(context.Background(), "", "toggle me"); err != nil {
		t.Fatal(err)
	}
	m := New(Options{Home: "/x", GoalService: svc})
	m.input.SetValue("/goal")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if mm.mode != modeMenu || mm.menu.kind != menuGoal {
		t.Fatalf("not goal menu: mode=%v kind=%v", mm.mode, mm.menu.kind)
	}
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeySpace})
	mm = out.(Model)
	tasks, _ := svc.ListTasks(context.Background(), "")
	if len(tasks) != 1 || tasks[0].Status != goal.TaskDone {
		t.Fatalf("task not toggled done: %+v", tasks)
	}
}

func TestUpdate_EnterStartsRunWhenAgentSet(t *testing.T) {
	a := scriptedAgent{n: "stub", events: []agent.Event{
		agent.MessageEvent{Text: "hi"},
		agent.DoneEvent{Usage: agent.Usage{}},
	}}
	m := New(Options{Home: "/x", Agent: a})
	m.input.SetValue("hello")
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if !mm.busy {
		t.Fatal("model should be busy after enter")
	}
	if mm.current != "" {
		t.Fatalf("current = %q, want empty", mm.current)
	}
	if cmd == nil {
		t.Fatal("expected runStartMsg cmd")
	}
	// The cmd should produce a runStartMsg containing the channel.
	msg := cmd()
	rs, ok := msg.(runStartMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want runStartMsg", msg)
	}
	if rs.ch == nil {
		t.Fatal("runStartMsg.ch is nil")
	}
}

func TestUpdate_EnterEmptyIgnored(t *testing.T) {
	m := New(Options{Home: "/x"})
	m.input.SetValue("   ")
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if mm.busy {
		t.Fatal("empty input should not start a run")
	}
	if cmd != nil {
		t.Fatal("empty input should not produce a cmd")
	}
}

func TestUpdate_EnterWithoutAgentShowsError(t *testing.T) {
	m := New(Options{Home: "/x"})
	m.input.SetValue("hi")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if mm.busy {
		t.Fatal("should not start without an agent")
	}
	if !strings.Contains(mm.transcript.String(), "no agent wired") {
		t.Fatalf("expected error message, got %q", mm.transcript.String())
	}
}

func TestUpdate_BusyIgnoresInput(t *testing.T) {
	a := scriptedAgent{n: "stub", events: []agent.Event{agent.DoneEvent{}}}
	m := New(Options{Home: "/x", Agent: a})
	m.busy = true
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	mm := out.(Model)
	if mm.input.Value() != "" {
		t.Fatalf("input should not accept text while busy, got %q", mm.input.Value())
	}
}

func TestUpdate_PrintableCharGoesToInput(t *testing.T) {
	m := New(Options{Home: "/x"})
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	out, _ = out.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	mm := out.(Model)
	if mm.input.Value() != "hi" {
		t.Fatalf("input = %q, want hi", mm.input.Value())
	}
}

// TestStatusRefreshMsg_RerendersFooter covers the Codex `limit:` HUD
// bug: the footer status line is pull-based (statusFn is read inside
// View), so a snapshot that arrives from a background goroutine does
// not appear until something forces a re-render. A statusRefreshMsg
// must trigger that re-render without mutating any model state and
// without scheduling further work.
func TestStatusRefreshMsg_RerendersFooter(t *testing.T) {
	// limitVisible flips from false->true to simulate the async Codex
	// usage snapshot landing after a model swap.
	limitVisible := false
	m := New(Options{
		Home: "/x",
		StatusFn: func() string {
			if limitVisible {
				return "limit: 5h 42% │ wk 10%"
			}
			return ""
		},
	})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = out.(Model)

	if strings.Contains(m.View(), "limit:") {
		t.Fatal("limit tile should be absent before the snapshot arrives")
	}

	// Snapshot arrives in the background; nothing has redrawn yet.
	limitVisible = true

	out, cmd := m.Update(StatusRefreshMsgValue())
	mm := out.(Model)
	if cmd != nil {
		t.Fatal("statusRefreshMsg must not schedule a follow-up command")
	}
	if mm.busy || mm.quitting {
		t.Fatal("statusRefreshMsg must not mutate run state")
	}
	if !strings.Contains(mm.View(), "limit: 5h 42%") {
		t.Fatalf("limit tile should appear after redraw, view=%q", mm.View())
	}
}

func TestUpdate_WindowSizeResizesViewport(t *testing.T) {
	m := New(Options{Home: "/x"})
	out, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	mm := out.(Model)
	if mm.width != 120 || mm.height != 40 {
		t.Fatalf("size = %dx%d", mm.width, mm.height)
	}
	if mm.viewport.Width != 120 {
		t.Fatalf("viewport.Width = %d", mm.viewport.Width)
	}
	// 40 rows minus chrome: header, separator, input box
	// (1 line + 2 border rows), key hints, status line.
	if mm.viewport.Height != 32 {
		t.Fatalf("viewport.Height = %d, want 32", mm.viewport.Height)
	}
}

func TestUpdate_RunEventMessageAppendsText(t *testing.T) {
	a := scriptedAgent{n: "stub", events: []agent.Event{
		agent.MessageEvent{Text: "hello "},
		agent.MessageEvent{Text: "world"},
		agent.DoneEvent{},
	}}
	m := New(Options{Home: "/x", Agent: a})
	m.input.SetValue("hi")
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if !mm.busy {
		t.Fatal("expected busy")
	}
	// Drive the agent's channel through the TUI manually.
	ch := cmd().(runStartMsg).ch
	// First event: runEventMsg(MessageEvent "hello ")
	out, cmd = mm.Update(runEventMsg{ev: <-ch})
	mm = out.(Model)
	if !mm.busy {
		t.Fatal("still busy after first event")
	}
	if !strings.Contains(mm.current, "hello ") {
		t.Fatalf("current = %q", mm.current)
	}
	// Second event: runEventMsg(MessageEvent "world")
	out, cmd = mm.Update(runEventMsg{ev: <-ch})
	mm = out.(Model)
	if !strings.Contains(mm.current, "world") {
		t.Fatalf("current = %q", mm.current)
	}
	// Third event: DoneEvent
	out, cmd = mm.Update(runEventMsg{ev: <-ch})
	mm = out.(Model)
	// DoneEvent should produce runEndMsg cmd.
	if cmd == nil {
		t.Fatal("expected cmd from DoneEvent")
	}
	if _, ok := cmd().(runEndMsg); !ok {
		t.Fatalf("cmd() = %T, want runEndMsg", cmd())
	}
}

func TestUpdate_RunEventEndResetsBusy(t *testing.T) {
	m := New(Options{Home: "/x"})
	m.busy = true
	out, _ := m.Update(runEndMsg{})
	mm := out.(Model)
	if mm.busy {
		t.Fatal("busy should reset on runEndMsg")
	}
	if mm.input.Value() != "" {
		t.Fatal("input should be reset")
	}
}

func TestUpdate_ToolCallAndResultRendered(t *testing.T) {
	a := scriptedAgent{n: "stub", events: []agent.Event{
		agent.ToolCallEvent{Name: "read_image", Args: `{"path":"x.png"}`, ID: "1"},
		agent.ToolResultEvent{ID: "1", Output: "loaded x.png"},
		agent.DoneEvent{},
	}}
	m := New(Options{Home: "/x", Agent: a})
	m.input.SetValue("load")
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	ch := cmd().(runStartMsg).ch
	out, _ = mm.Update(runEventMsg{ev: <-ch})
	mm = out.(Model)
	if !strings.Contains(mm.transcript.String(), "read_image") {
		t.Fatalf("transcript missing tool name: %q", mm.transcript.String())
	}
	out, _ = mm.Update(runEventMsg{ev: <-ch})
	mm = out.(Model)
	if !strings.Contains(mm.transcript.String(), "loaded x.png") {
		t.Fatalf("transcript missing tool output: %q", mm.transcript.String())
	}
}

func TestView_QuittingShowsBye(t *testing.T) {
	m := New(Options{Home: "/x"})
	m.quitting = true
	v := m.View()
	if !strings.Contains(v, "closed") {
		t.Fatalf("expected closed message, got %q", v)
	}
}

func TestView_ShowsLLMNameInHeader(t *testing.T) {
	a, _ := newStubAgent("planner")
	l, _ := newStubLLM("noop-model")
	m := New(Options{Home: "/x", Agent: a, LLM: l})
	v := m.View()
	if !strings.Contains(v, "noop-model") {
		t.Fatalf("missing llm model name: %q", v)
	}
}

func TestView_BusyShowsSpinner(t *testing.T) {
	m := New(Options{Home: "/x"})
	m.busy = true
	m.current = "typing..."
	m.refreshTranscript()
	v := m.View()
	if !strings.Contains(v, "typing...") {
		t.Fatalf("view missing current text: %q", v)
	}
}

func TestWelcome_IncludesModelName(t *testing.T) {
	a, _ := newStubAgent("planner")
	l, _ := newStubLLM("gpt-x")
	w := welcome(Options{Home: "/x", Agent: a, LLM: l}, DefaultPalette())
	if !strings.Contains(w, "gpt-x") {
		t.Fatalf("welcome missing model: %q", w)
	}
}

func TestAppendLine(t *testing.T) {
	m := New(Options{Home: "/x"})
	m.appendLine("a")
	m.appendLine("b")
	if got := m.transcript.String(); got != "a\nb\n" {
		t.Fatalf("transcript = %q", got)
	}
}

func TestFlushCurrent(t *testing.T) {
	m := New(Options{Home: "/x"})
	m.current = "streaming"
	m.flushCurrent()
	if m.current != "" {
		t.Fatal("current should be cleared after flush")
	}
	if !strings.Contains(m.transcript.String(), "streaming") {
		t.Fatal("transcript should contain flushed text")
	}
}

func TestWaitForEvent_ClosedChannelProducesEndMsg(t *testing.T) {
	ch := make(chan agent.Event)
	close(ch)
	cmd := waitForEvent(ch)
	msg := cmd()
	if _, ok := msg.(runEndMsg); !ok {
		t.Fatalf("msg = %T, want runEndMsg", msg)
	}
}

func TestWaitForEvent_DeliversEvent(t *testing.T) {
	ch := make(chan agent.Event, 1)
	ch <- agent.MessageEvent{Text: "x"}
	cmd := waitForEvent(ch)
	msg := cmd()
	re, ok := msg.(runEventMsg)
	if !ok {
		t.Fatalf("msg = %T, want runEventMsg", msg)
	}
	if re.ev.(agent.MessageEvent).Text != "x" {
		t.Fatalf("ev text = %q", re.ev.(agent.MessageEvent).Text)
	}
}

func TestWaitForEvent_NilReturnsNil(t *testing.T) {
	if cmd := waitForEvent(nil); cmd != nil {
		t.Fatal("nil channel should produce nil cmd")
	}
}

// F28: /cost renders cost dashboard from stats recorder.
func TestCostCommand_RendersDashboard(t *testing.T) {
	rec := newMockStatsRecorder()
	// Simulate a few turns.
	rec.StartStep(1)
	rec.RecordTokens(500, 200)
	rec.RecordModel("gpt-4o")
	rec.EndStep()
	rec.StartStep(2)
	rec.RecordTokens(300, 150)
	rec.RecordModel("gpt-4o")
	rec.EndStep()

	m := New(Options{
		Home:          "/x",
		StatsRecorder: rec,
	})
	m.input.SetValue("/cost")
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	// Built-in commands are synchronous — cmd should be non-nil.
	if cmd == nil {
		t.Fatal("/cost should return a command")
	}
	msg := cmd()
	res, ok := msg.(slashResultMsg)
	if !ok {
		t.Fatalf("/cost cmd() = %T, want slashResultMsg", msg)
	}
	if res.Err != nil {
		t.Fatalf("/cost error: %v", res.Err)
	}
	if !strings.Contains(res.Body, "Cost Dashboard") {
		t.Fatalf("/cost output missing 'Cost Dashboard': %q", res.Body)
	}
	_ = mm
}

// F28: /cost without stats recorder returns "not available".
func TestCostCommand_NoRecorder(t *testing.T) {
	m := New(Options{
		Home: "/x",
		// StatsRecorder intentionally nil.
	})
	m.input.SetValue("/cost")
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if cmd == nil {
		t.Fatal("/cost should return a command")
	}
	msg := cmd()
	res, ok := msg.(slashResultMsg)
	if !ok {
		t.Fatalf("/cost cmd() = %T, want slashResultMsg", msg)
	}
	if !strings.Contains(res.Body, "stats not available") {
		t.Fatalf("/cost should report unavailable: %q", res.Body)
	}
	_ = mm
}

func TestHandleEscCancel(t *testing.T) {
	m := New(Options{Home: "/x"})
	// Arm a cancel that records invocation.
	invoked := false
	m.cancel.Arm(cancelRun, func() { invoked = true })
	m.busy = true

	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	mm := out.(Model)
	if mm.busy {
		t.Fatal("ESC should clear busy")
	}
	if !invoked {
		t.Fatal("ESC cancel should invoke CancelFunc")
	}
	// statusOverride should be "cancelled".
	if mm.statusOverride != "cancelled" {
		t.Fatalf("statusOverride = %q, want cancelled", mm.statusOverride)
	}
	// cmd should be non-nil (timer).
	if cmd == nil {
		t.Fatal("ESC cancel should return a timer cmd")
	}
}

func TestStatusOverrideClear(t *testing.T) {
	m := New(Options{Home: "/x"})
	m.statusOverride = "cancelled"
	out, _ := m.Update(statusOverrideClearMsg{})
	mm := out.(Model)
	if mm.statusOverride != "" {
		t.Fatalf("statusOverride after clear = %q, want empty", mm.statusOverride)
	}
}

func TestEscWhenIdle(t *testing.T) {
	m := New(Options{Home: "/x"})
	m.busy = false
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	mm := out.(Model)
	// ESC when idle does nothing (clears input or shows hint).
	if mm.busy {
		t.Fatal("ESC when idle should not set busy")
	}
}

// TestMessageEventStream verifies that multiple consecutive
// MessageEvents are all accumulated and View() reflects each.
// Regression: waitForEvent(nil) returned nil, stopping the stream.
func TestMessageEventStream(t *testing.T) {
	m := New(Options{Home: "/x"})

	ch := make(chan agent.Event, 10)
	out, cmd := m.Update(runStartMsg{ch: ch})
	m = out.(Model)

	if !m.busy {
		t.Fatal("expected busy after runStartMsg")
	}
	if m.eventCh != ch {
		t.Fatal("eventCh not stored")
	}

	// runStartMsg returns tea.Batch(waitForEvent, streamFlushCmd).
	// Calling cmd() directly returns tea.BatchMsg; the waitForEvent
	// goroutine reads from ch and sends to Bubble Tea's queue.
	// In tests we read from ch directly and wrap as runEventMsg.
	_ = cmd() // start batch goroutines (returns tea.BatchMsg)

	// Send 3 MessageEvents.
	ch <- agent.MessageEvent{Text: "The "}
	ch <- agent.MessageEvent{Text: "quick "}
	ch <- agent.MessageEvent{Text: "fox"}
	close(ch)

	// Read 3 events from ch directly.
	for i := 0; i < 3; i++ {
		ev, ok := <-ch
		if !ok {
			t.Fatalf("step %d: channel closed too early", i)
		}
		out, cmd = m.Update(runEventMsg{ev: ev})
		m = out.(Model)
	}

	// After 3 MessageEvents, m.current should have all 3 texts.
	if m.current != "The quick fox" {
		t.Fatalf("m.current = %q, want %q", m.current, "The quick fox")
	}

	// View() must contain the streaming text BEFORE DoneEvent.
	view := m.View()
	if !strings.Contains(view, "The quick fox") {
		t.Fatalf("View() before DoneEvent does not contain streaming text.\nView():\n%s", view)
	}

	// cmd from last Update is m.waitForNextEvent() → blocking read.
	// Channel is closed, so it returns runEndMsg.
	msg := cmd()
	if msg == nil {
		t.Fatal("last cmd returned nil, expected runEndMsg from closed channel")
	}
	if _, ok := msg.(runEndMsg); !ok {
		t.Fatalf("expected runEndMsg after channel close, got %T", msg)
	}
}

// TestMessageEventStream_LiveView verifies that View() reflects
// each token as it arrives — the user sees text streaming live
// token-by-token, not all at once after DoneEvent.
func TestMessageEventStream_LiveView(t *testing.T) {
	m := New(Options{Home: "/x"})

	ch := make(chan agent.Event, 10)
	out, _ := m.Update(runStartMsg{ch: ch})
	m = out.(Model)

	// Step 1
	ch <- agent.MessageEvent{Text: "Hello "}
	ev := <-ch
	out, _ = m.Update(runEventMsg{ev: ev})
	m = out.(Model)
	if !strings.Contains(m.View(), "Hello ") {
		t.Fatal("View() after step 1 should contain 'Hello '")
	}

	// Step 2
	ch <- agent.MessageEvent{Text: "World"}
	ev = <-ch
	out, _ = m.Update(runEventMsg{ev: ev})
	m = out.(Model)
	if !strings.Contains(m.View(), "Hello World") {
		t.Fatalf("View() after step 2 should contain 'Hello World'\nView():\n%s", m.View())
	}

	// Step 3
	ch <- agent.MessageEvent{Text: "!"}
	ev = <-ch
	out, _ = m.Update(runEventMsg{ev: ev})
	m = out.(Model)
	if !strings.Contains(m.View(), "Hello World!") {
		t.Fatalf("View() after step 3 should contain 'Hello World!'\nView():\n%s", m.View())
	}

	close(ch)
}
