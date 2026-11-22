package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestIsStatusQuestion(t *testing.T) {
	for _, tc := range []struct {
		prompt string
		want   bool
	}{
		{"i jak idzie?", true},
		{"jak idzie", true},
		{"co tam?", true},
		{"Status?", true},
		{"ok, jaki jest status?", true},
		{"How's it going?", true},
		{"any updates?", true},
		{"progress?", true},
		{"i jak idzie? kontynuuj", false},
		{"co tam, wznów worker-306", false},
		{"status bar fix", false},
		{"update the readme", false},
		{"napraw build", false},
		{"jak działa ten kod?", false},
		{"jak idzie praca nad tym wielkim refaktorem modułu zapisu świata", false},
		{"", false},
		// Review 1.0.39: mixed status + instruction, and status words used
		// in an ordinary question, are normal messages (never blocked).
		{"status: przetestuj build", false},
		{"jak tam, napisz testy do parsera", false},
		{"what's the status of the build, deploy it", false},
		{"jak idzie? i przy okazji zbadaj błąd", false},
		{"co tam jest w pliku config.go?", false},
		{"jak tam działa ta funkcja?", false},
		{"status of the deploy", false},
		{"jak idzie budowanie? popraw testy", false},
		{"jak idzie z tym?", true},
		{"how is it going so far?", true},
		{"no i co tam", true},
	} {
		if got := isStatusQuestion(tc.prompt); got != tc.want {
			t.Errorf("isStatusQuestion(%q) = %v, want %v", tc.prompt, got, tc.want)
		}
	}
}

type statusTurnFixture struct {
	coordinator *stubProvider
	child       *stubProvider
	parent      *Loop
	task        *AgentTool
	writer      *recordingWriter
}

func newStatusTurnFixture(t *testing.T, coordinatorScripts, childScripts [][]llm.Delta) statusTurnFixture {
	t.Helper()
	reg := tools.NewRegistry()
	coordinator := &stubProvider{name: "anthropic-coordinator", scripts: coordinatorScripts}
	writer := &recordingWriter{}
	parent, err := NewLoop(LoopConfig{Provider: coordinator, Registry: reg, Writer: writer, MaxSteps: 8})
	if err != nil {
		t.Fatal(err)
	}
	// Workers on another backend than the coordinator, as in the reported
	// session (Claude coordinator, GPT workers).
	child := &stubProvider{name: "openai-worker", scripts: childScripts}
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	task, err := NewAgentTool(specs, parent, reg, child, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	task.WorkerProvider = child
	reg.MustRegister(task.Spec())
	reg.MustRegister(NewSendMessageToolFor(task).Spec())
	reg.MustRegister(NewTaskStopTool(task.Workers).Spec())
	reg.MarkAlwaysOn("task")
	reg.MarkAlwaysOn("send_message")
	reg.MarkAlwaysOn("task_stop")
	return statusTurnFixture{coordinator: coordinator, child: child, parent: parent, task: task, writer: writer}
}

func runTurnText(t *testing.T, l *Loop, prompt string) string {
	t.Helper()
	events, err := l.Run(context.Background(), prompt)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	done := false
	for ev := range events {
		switch ev := ev.(type) {
		case MessageEvent:
			text.WriteString(ev.Text)
		case DoneEvent:
			done = true
		case ErrorEvent:
			t.Errorf("run error: %v", ev.Err)
		}
	}
	if !done {
		t.Fatalf("turn %q did not complete", prompt)
	}
	return text.String()
}

// Reproduction of the reported session: workers exist (one done, then failed
// with "context canceled"), the user asks only "i jak idzie?", and the
// coordinator answered by resuming the worker and starting another. Now the
// turn carries the workers' states, delegation calls are refused, no worker
// is created or resumed, and the answer is a status report.
func TestStatusQuestionDoesNotStartOrResumeWorkers(t *testing.T) {
	f := newStatusTurnFixture(t,
		[][]llm.Delta{
			// Turn 1: the user's real request starts a worker.
			{{ToolCall: &llm.ToolCall{ID: "t1", Name: "task", Arguments: `{"agent":"code","prompt":"add hostile mobs to the overworld"}`}, FinishReason: "tool_calls"}},
			{{Content: "Delegated to worker-1.", FinishReason: "stop"}},
			// Turn 2 ("i jak idzie?"): the model tries what it did in the
			// session — resume the failed worker and start another one.
			{
				{ToolCall: &llm.ToolCall{ID: "s1", Name: "send_message", Arguments: `{"to":"worker-1","message":"Wznowienie: kontynuuj zadanie pająk/szkielet/creeper."}`}},
				{ToolCall: &llm.ToolCall{ID: "s2", Name: "task", Arguments: `{"agent":"code","prompt":"continue hostile mobs"}`}, FinishReason: "tool_calls"},
			},
			{{Content: "Status: worker-1 nie dokończył zadania (context canceled). Mogę go wznowić, jeśli chcesz.", FinishReason: "stop"}},
		},
		[][]llm.Delta{{{Content: "HOSTILE_MOBS_PARTIAL: spider done", FinishReason: "stop"}}},
	)
	runTurnText(t, f.parent, "Dodaj pająka, szkielet i creepera w zwykłym świecie.")
	workers := f.task.Workers.List()
	if len(workers) != 1 {
		t.Fatalf("setup: workers = %d, want 1", len(workers))
	}
	w := workers[0]
	// The worker's run was then canceled (the user's Stop), as in the session.
	w.setState(func(w *Worker) { w.Status, w.LastError = "failed", "context canceled" })
	childCalls := atomic.LoadInt32(&f.child.calls)
	coordinatorCalls := atomic.LoadInt32(&f.coordinator.calls)

	answer := runTurnText(t, f.parent, "i jak idzie?")

	if got := len(f.task.Workers.List()); got != 1 {
		t.Fatalf("status question created a worker: workers = %d, want 1", got)
	}
	if s := w.Snapshot(); s.Runs != 1 || s.Status != "failed" {
		t.Fatalf("status question resumed the worker: runs=%d status=%s", s.Runs, s.Status)
	}
	if got := atomic.LoadInt32(&f.child.calls); got != childCalls {
		t.Fatalf("worker model was called %d more time(s) during a status question", got-childCalls)
	}
	// One request with the refused calls, one for the answer; no completion
	// check pass even though the answer admits unfinished work.
	if got := atomic.LoadInt32(&f.coordinator.calls) - coordinatorCalls; got != 2 {
		t.Fatalf("status turn coordinator calls = %d, want 2", got)
	}
	first := f.coordinator.reqs[coordinatorCalls]
	if !requestContains(first, "[status question") || !requestContains(first, "worker-1 (code) failed, runs 1") || !requestContains(first, "context canceled") {
		t.Fatal("status turn request does not carry the workers' current states")
	}
	refused := 0
	for _, m := range f.coordinator.reqs[coordinatorCalls+1] {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "refused: the user only asked for a status update") {
			refused++
		}
	}
	if refused != 2 {
		t.Fatalf("refused delegation results = %d, want 2", refused)
	}
	if !strings.Contains(answer, "Status: worker-1") {
		t.Fatalf("answer is not a status report: %q", answer)
	}
	for _, m := range f.writer.messages {
		if strings.Contains(m.Content, "[completion check]") {
			t.Fatal("completion check pushed a status answer into more work")
		}
	}

	// The guard is per turn: an explicit instruction resumes the worker.
	f.coordinator.scripts = append(f.coordinator.scripts,
		[]llm.Delta{{ToolCall: &llm.ToolCall{ID: "s3", Name: "send_message", Arguments: `{"to":"worker-1","message":"continue with the skeleton"}`}, FinishReason: "tool_calls"}},
		[]llm.Delta{{Content: "Resumed worker-1.", FinishReason: "stop"}},
	)
	runTurnText(t, f.parent, "wznów worker-1")
	if s := w.Snapshot(); s.Runs != 2 {
		t.Fatalf("explicit resume: runs = %d, want 2", s.Runs)
	}
}

// Resume/stop of a failed or stopped worker in a user turn needs a message
// that names the worker without a negation (or the UI button, whose message
// names it). Keyword guesses are not a go-ahead.
func TestNamesWorkerWithoutNegation(t *testing.T) {
	for _, tc := range []struct {
		prompt, action string
		want           bool
	}{
		{"wznów worker-7", "resume", true},
		{"Wznów worker-7 przez send_message i kontynuuj jego zadanie od miejsca przerwania; jego zapisana rozmowa zostanie przywrócona.", "resume", true},
		{"kontynuuj workera 7", "resume", true},
		{"resume worker 7 please", "resume", true},
		{"nie wznawiaj go", "resume", false},
		{"nie wznawiaj worker-7", "resume", false},
		{"don't resume worker-7", "resume", false},
		{"tak, ale co to za plik?", "resume", false},
		{"ile to 7 plików?", "resume", false},
		{"wznów go", "resume", false},
		{"worker-7 nie, ale worker-8 tak", "resume", false},
		{"zatrzymaj worker-7", "stop", true},
		{"zatrzymaj worker-7", "resume", false},
		{"nie zatrzymuj worker-7", "stop", false},
		{"co robi worker-70?", "resume", false},
	} {
		if got := namesWorkerWithoutNegation(tc.prompt, "worker-7", tc.action); got != tc.want {
			t.Errorf("%s %q = %v, want %v", tc.action, tc.prompt, got, tc.want)
		}
	}
}
