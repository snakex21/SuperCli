package agent

// Orchestrator contract (1.0.39), one deterministic test per case. Every
// provider here is a scripted fake; no test calls a real API.
//
//   - delegated work runs in the background where reports wake the
//     conversation: the coordinator's turn returns at once;
//   - the user's next message or Stop of the turn never cancels a worker;
//   - a failed, interrupted or stopped worker resumes only on the user's word;
//   - automatic turns report, they do not start the work again;
//   - a background worker's question reaches the conversation;
//   - the model's words never decide a worker's state.

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func tcall(id, name, args string) llm.Delta {
	return llm.Delta{ToolCall: &llm.ToolCall{ID: id, Name: name, Arguments: args}}
}

func callStep(calls ...llm.Delta) []llm.Delta {
	calls[len(calls)-1].FinishReason = "tool_calls"
	return calls
}

func say(text string) []llm.Delta { return []llm.Delta{{Content: text, FinishReason: "stop"}} }

// contractFixture: a coordinator with task/send_message/task_stop/ask_user and
// a worker backend; wake=true installs the host wake-up policy (GUI, TUI).
func contractFixture(t *testing.T, wake bool, coordinator, worker [][]llm.Delta) (statusTurnFixture, chan Event) {
	t.Helper()
	f := newStatusTurnFixture(t, coordinator, worker)
	reg := f.parent.registry
	reg.MustRegister(tools.NewAskUser(nil).Spec())
	ext := make(chan Event, 512)
	f.parent.SetExternalSink(ext)
	if wake {
		f.parent.SetBackgroundWake(WakeConfig{Debounce: testWakeDebounce})
	}
	return f, ext
}

func runTurnWithin(t *testing.T, l *Loop, prompt string, within time.Duration) string {
	t.Helper()
	done := make(chan string, 1)
	go func() { done <- runTurnText(t, l, prompt) }()
	select {
	case text := <-done:
		return text
	case <-time.After(within):
		t.Fatalf("turn %q did not return within %v (it waited for a worker)", prompt, within)
		return ""
	}
}

func waitWorker(t *testing.T, w *Worker, what string, ok func(Snapshot) bool) Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s := w.Snapshot()
		if ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker %s: %s; status=%s runs=%d err=%q", w.ID, what, s.Status, s.Runs, s.LastError)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// task without async:true still runs in the background where reports wake
// the conversation (before: the turn waited for the whole worker run).
func TestContractTaskRunsInBackgroundWithoutAsyncFlag(t *testing.T) {
	release := make(chan struct{})
	f, ext := contractFixture(t, true,
		[][]llm.Delta{callStep(tcall("t1", "task", `{"prompt":"build the skeleton mob"}`)), say("Delegated to worker-1.")},
		[][]llm.Delta{say("SKELETON_DONE")})
	f.child.onCalled = func(int) { <-release }
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	runTurnWithin(t, f.parent, "Zrób szkieleta.", 3*time.Second)
	w := f.task.Workers.List()[0]
	waitWorker(t, w, "should be running in the background", func(s Snapshot) bool { return s.Status == "running" })
	close(release)
	if _, ok := waitWakeEvent(t, ext, 5*time.Second); !ok {
		t.Fatal("the finished worker did not wake the conversation")
	}
	waitWorker(t, w, "should finish", func(s Snapshot) bool { return s.Status == "done" })
	if !requestContains(f.parent.Messages, "SKELETON_DONE") {
		t.Fatal("the worker's report was not delivered to the conversation")
	}
}

// send_message continuation runs in the background too (the reported
// session: an automatic turn waited 14 minutes on worker-306).
func TestContractContinuationRunsInBackground(t *testing.T) {
	release := make(chan struct{})
	f, ext := contractFixture(t, true,
		[][]llm.Delta{
			callStep(tcall("t1", "task", `{"prompt":"add hostile mobs"}`)), say("Delegated."),
			callStep(tcall("s1", "send_message", `{"to":"worker-1","message":"continue with the skeleton"}`)), say("worker-1 continues in the background."),
		},
		[][]llm.Delta{say("spider done"), say("SKELETON_DONE")})
	f.child.onCalled = func(n int) {
		if n == 1 {
			<-release
		}
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	runTurnWithin(t, f.parent, "Dodaj moby.", 3*time.Second)
	w := f.task.Workers.List()[0]
	waitWorker(t, w, "first run should finish", func(s Snapshot) bool { return s.Status == "done" })
	drainExt(ext)
	runTurnWithin(t, f.parent, "kontynuuj worker-1: szkielet", 3*time.Second)
	waitWorker(t, w, "continuation should run in the background", func(s Snapshot) bool { return s.Status == "running" && s.Runs == 2 })
	close(release)
	if _, ok := waitWakeEvent(t, ext, 5*time.Second); !ok {
		t.Fatal("the continued worker's report did not wake the conversation")
	}
	waitWorker(t, w, "continuation should finish", func(s Snapshot) bool { return s.Status == "done" })
	if !requestContains(f.parent.Messages, "SKELETON_DONE") {
		t.Fatal("the continued worker's report was not delivered")
	}
}

func drainExt(ch chan Event) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// Stop of the coordinator's turn (or "interrupt and send") cancels the turn,
// never the worker it started.
func TestContractTurnCancelDoesNotStopBackgroundWorker(t *testing.T) {
	release := make(chan struct{})
	f, _ := contractFixture(t, true,
		[][]llm.Delta{callStep(tcall("t1", "task", `{"prompt":"compile the project"}`)), say("Delegated.")},
		[][]llm.Delta{say("BUILD_OK")})
	f.child.onCalled = func(int) { <-release }
	ctx, cancel := context.WithCancel(context.Background())
	events, err := f.parent.Run(ctx, "Skompiluj projekt.")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	w := f.task.Workers.List()[0]
	waitWorker(t, w, "should be running", func(s Snapshot) bool { return s.Status == "running" })
	cancel() // the user's Stop / next message ends the turn's context
	time.Sleep(50 * time.Millisecond)
	if s := w.Snapshot(); s.Status != "running" {
		t.Fatalf("canceling the turn changed the worker: %s (%s)", s.Status, s.LastError)
	}
	close(release)
	waitWorker(t, w, "should finish on its own", func(s Snapshot) bool { return s.Status == "done" })
}

// An automatic turn never resumes a failed or stopped worker (the session:
// "Wznowienie po błędzie połączenia: kontynuuj..." with no user message).
func TestContractWakeTurnDoesNotResumeFailedWorker(t *testing.T) {
	f, _ := contractFixture(t, true,
		[][]llm.Delta{
			callStep(tcall("t1", "task", `{"prompt":"spider, skeleton, creeper"}`)), say("Delegated."),
			// The automatic turn tries to resume the failed worker.
			callStep(tcall("s1", "send_message", `{"to":"worker-1","message":"Wznowienie po błędzie połączenia: kontynuuj."}`)),
			say("worker-1 zakończył się błędem połączenia. Mogę go wznowić, jeśli chcesz."),
		},
		[][]llm.Delta{{{Content: "partial", FinishReason: "stop"}}})
	runTurnWithin(t, f.parent, "Dodaj moby.", 3*time.Second)
	w := f.task.Workers.List()[0]
	waitWorker(t, w, "first run should end", func(s Snapshot) bool { return s.Status == "done" })
	w.setState(func(w *Worker) { w.Status, w.LastError = "failed", "sse: stream error: INTERNAL_ERROR" })
	childCalls := atomic.LoadInt32(&f.child.calls)
	f.parent.DeliverWorkerReport(context.Background(), "<task-notification><task-id>worker-1</task-id><status>failed</status></task-notification>")
	events, err := f.parent.RunWake(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	refused := false
	for ev := range events {
		if r, ok := ev.(ToolResultEvent); ok && r.Err != nil && strings.Contains(r.Err.Error(), "automatic turn without a user message") {
			refused = true
		}
	}
	if !refused {
		t.Fatal("the automatic turn's resume was not refused")
	}
	if s := w.Snapshot(); s.Runs != 1 || atomic.LoadInt32(&f.child.calls) != childCalls {
		t.Fatalf("the automatic turn resumed the worker: runs=%d", s.Runs)
	}
}

// In a user turn, a stopped/failed worker resumes only when the message asks
// for it; an unrelated question does not restart it.
func TestContractUserTurnResumesOnlyWhenAsked(t *testing.T) {
	f, _ := contractFixture(t, false,
		[][]llm.Delta{
			callStep(tcall("t1", "task", `{"prompt":"spider"}`)), say("Delegated."),
			callStep(tcall("s1", "send_message", `{"to":"worker-1","message":"continue"}`)), say("Plik zawiera konfigurację."),
			callStep(tcall("s2", "send_message", `{"to":"worker-1","message":"continue"}`)), say("Wznowiłem worker-1."),
		},
		[][]llm.Delta{say("spider partial"), say("spider done")})
	runTurnText(t, f.parent, "Dodaj pająka.")
	w := f.task.Workers.List()[0]
	w.setState(func(w *Worker) { w.Status, w.LastError = "stopped", "stopped by request" })
	runTurnText(t, f.parent, "co jest w pliku config.toml?")
	if s := w.Snapshot(); s.Runs != 1 || s.Status != "stopped" {
		t.Fatalf("an unrelated question resumed a stopped worker: runs=%d status=%s", s.Runs, s.Status)
	}
	runTurnText(t, f.parent, "wznów go")
	if s := w.Snapshot(); s.Runs != 2 {
		t.Fatalf("an explicit resume did not run: runs=%d", s.Runs)
	}
}

// An automatic turn reporting "worker nie dokończył" is not asked by the
// completion check to finish the work itself.
func TestContractWakeTurnSkipsCompletionCheck(t *testing.T) {
	f, _ := contractFixture(t, true,
		[][]llm.Delta{say("Worker-1 nie dokończył zadania: build is still missing two files.")}, nil)
	f.parent.completionCheck = true
	f.parent.Messages = append(f.parent.Messages, llm.Message{Role: llm.RoleUser, Content: "Zrób to."}, llm.Message{Role: llm.RoleAssistant, Content: "Delegated."})
	f.parent.DeliverWorkerReport(context.Background(), "<task-notification><task-id>worker-1</task-id><status>done</status><result>half done</result></task-notification>")
	events, err := f.parent.RunWake(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	for _, m := range f.parent.Messages {
		if strings.Contains(m.Content, "[completion check]") {
			t.Fatal("completion check turned an automatic report into more work")
		}
	}
	if got := atomic.LoadInt32(&f.coordinator.calls); got != 1 {
		t.Fatalf("automatic turn calls = %d, want 1", got)
	}
}

// Two workers that finish at the same moment both reach the conversation.
func TestContractTwoWorkersFinishingTogetherAreBothDelivered(t *testing.T) {
	release := make(chan struct{})
	f, ext := contractFixture(t, true,
		[][]llm.Delta{
			callStep(tcall("t1", "task", `{"prompt":"A: spider"}`), tcall("t2", "task", `{"prompt":"B: skeleton"}`)),
			say("Two workers started."),
			say("Both finished."),
		},
		[][]llm.Delta{say("REPORT_X"), say("REPORT_X")})
	f.child.onCalled = func(int) { <-release }
	runTurnWithin(t, f.parent, "Zrób oba.", 3*time.Second)
	if n := len(f.task.Workers.List()); n != 2 {
		t.Fatalf("workers = %d", n)
	}
	close(release)
	for _, w := range f.task.Workers.List() {
		waitWorker(t, w, "should finish", func(s Snapshot) bool { return s.Status == "done" })
	}
	if _, ok := waitWakeEvent(t, ext, 5*time.Second); !ok {
		t.Fatal("no wake-up for the finished workers")
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.parent.UnseenBackgroundReports() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := f.parent.UnseenBackgroundReports(); n != 2 {
		t.Fatalf("unseen reports = %d, want 2", n)
	}
}

// A background worker's question goes to the conversation's durable route,
// after the turn that started it has ended (before: it hung unread).
func TestContractBackgroundWorkerQuestionReachesConversation(t *testing.T) {
	f, _ := contractFixture(t, true,
		[][]llm.Delta{callStep(tcall("t1", "task", `{"prompt":"pick a texture"}`)), say("Delegated.")},
		[][]llm.Delta{
			callStep(tcall("q1", "ask_user", `{"question":"Which texture?","options":[{"label":"A"},{"label":"B"}]}`)),
			say("TEXTURE_B_USED"),
		})
	durable := make(chan tools.AskRequest, 1)
	ctx := tools.WithDurableAskChannel(context.Background(), durable)
	events, err := f.parent.Run(ctx, "Wybierz teksturę.")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	select {
	case req := <-durable:
		req.Respond <- tools.AskAnswer{Selected: []string{"B"}}
	case <-time.After(5 * time.Second):
		t.Fatal("the background worker's question never reached the conversation")
	}
	w := f.task.Workers.List()[0]
	waitWorker(t, w, "should finish after the answer", func(s Snapshot) bool { return s.Status == "done" })
}

// Two identical task calls in one response start one worker.
func TestContractDuplicateTaskIsRefused(t *testing.T) {
	f, _ := contractFixture(t, true,
		[][]llm.Delta{
			callStep(tcall("t1", "task", `{"prompt":"fix the build"}`), tcall("t2", "task", `{"prompt":"fix   the build"}`)),
			say("Delegated."),
		},
		[][]llm.Delta{say("ok")})
	release := make(chan struct{})
	defer close(release)
	f.child.onCalled = func(int) { <-release }
	runTurnWithin(t, f.parent, "Napraw build.", 3*time.Second)
	if n := len(f.task.Workers.List()); n != 1 {
		t.Fatalf("identical tasks started %d workers", n)
	}
	if !requestContains(f.coordinator.reqs[1], "already working on this exact task") {
		t.Fatal("the duplicate was not refused with a reason")
	}
}

// Greetings and thanks never start work.
func TestContractSmallTalkDoesNotDelegate(t *testing.T) {
	f, _ := contractFixture(t, false,
		[][]llm.Delta{callStep(tcall("t1", "task", `{"prompt":"say hi"}`)), say("Cześć!")}, nil)
	runTurnText(t, f.parent, "hej")
	if n := len(f.task.Workers.List()); n != 0 {
		t.Fatalf("a greeting started %d worker(s)", n)
	}
	for _, phrase := range []string{"dzięki!", "Cześć", "thanks", "siema"} {
		if !isSmallTalk(phrase) {
			t.Errorf("isSmallTalk(%q) = false", phrase)
		}
	}
	for _, phrase := range []string{"no", "ok", "tak", "ok, rób", "dzięki, teraz dodaj testy"} {
		if isSmallTalk(phrase) {
			t.Errorf("isSmallTalk(%q) = true (may approve a plan)", phrase)
		}
	}
}

// An invented worker id is an error, never a new worker.
func TestContractInventedWorkerIDIsAnError(t *testing.T) {
	f, _ := contractFixture(t, false,
		[][]llm.Delta{callStep(tcall("s1", "send_message", `{"to":"worker-306","message":"continue"}`)), say("Nie ma takiego workera.")}, nil)
	runTurnText(t, f.parent, "kontynuuj worker-306")
	if n := len(f.task.Workers.List()); n != 0 {
		t.Fatalf("an invented id created %d worker(s)", n)
	}
	if !requestContains(f.coordinator.reqs[1], "unknown worker") {
		t.Fatal("the invented id was not reported")
	}
}

// The coordinator saying "zrobione" does not make a failed worker done: the
// state comes from the worker's run.
func TestContractWorkerStateComesFromFactsNotWords(t *testing.T) {
	f, ext := contractFixture(t, true,
		[][]llm.Delta{callStep(tcall("t1", "task", `{"prompt":"compile"}`)), say("Zrobione, wszystko działa!")},
		nil)
	f.child.scripts = [][]llm.Delta{{{Content: "", FinishReason: ""}}}
	f.child.onCalled = func(int) {}
	failing := &erroringProvider{}
	f.task.WorkerProvider = failing
	runTurnWithin(t, f.parent, "Skompiluj.", 3*time.Second)
	w := f.task.Workers.List()[0]
	waitWorker(t, w, "should fail", func(s Snapshot) bool { return s.Status == "failed" })
	failedNotice := false
	deadline := time.After(5 * time.Second)
	for !failedNotice {
		select {
		case ev := <-ext:
			if n, ok := ev.(WorkerNotificationEvent); ok && n.Status == "failed" {
				failedNotice = true
			}
		case <-deadline:
			t.Fatal("no failed worker notification")
		}
	}
}

type erroringProvider struct{}

func (*erroringProvider) Name() string { return "failing-worker" }
func (*erroringProvider) Complete(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
	return nil, errFakeOutage
}

var errFakeOutage = &fakeOutageError{}

type fakeOutageError struct{}

func (*fakeOutageError) Error() string { return "fake provider outage (permanent, 400)" }

// A coordinator that answers nothing at all gets a visible notice.
func TestContractEmptyReplyIsReported(t *testing.T) {
	f, _ := contractFixture(t, false, [][]llm.Delta{{{FinishReason: "stop"}}}, nil)
	events, err := f.parent.Run(context.Background(), "napisz coś")
	if err != nil {
		t.Fatal(err)
	}
	noticed := false
	for ev := range events {
		if n, ok := ev.(NoticeEvent); ok && n.Text == EmptyReplyNotice {
			noticed = true
		}
	}
	if !noticed {
		t.Fatal("an empty reply ended the turn silently")
	}
}

func TestDestructiveCommandGuard(t *testing.T) {
	blocked := [][]string{
		{"rm", "-rf", "/"},
		{"rm", "-rf", "~"},
		{"rm", "-rf", "*"},
		{"rm", "-r", "-f", ".."},
		{"cmd", "/c", "rd /s /q C:\\"},
		{"cmd", "/c", "del /s /q C:\\*"},
		{"powershell", "-NoProfile", "-Command", "Remove-Item -Recurse -Force C:\\"},
		{"pwsh", "-c", "Get-ChildItem; Remove-Item -Recurse -Force $HOME"},
		{"bash", "-c", "cd /tmp && rm -rf /"},
		{"format", "D:"},
		{"diskpart"},
		{"shutdown", "/s", "/t", "0"},
		{"git", "push", "--force", "origin", "main"},
		{"git", "push", "origin", "+main"},
		{"git", "reset", "--hard", "HEAD~3"},
		{"git", "clean", "-fdx"},
		{"dd", "if=/dev/zero", "of=/dev/sda"},
	}
	for _, argv := range blocked {
		raw, _ := json.Marshal(map[string]any{"command": argv})
		if destructiveCommandRefusal("ctx_execute", string(raw)) == "" {
			t.Errorf("not blocked: %q", argv)
		}
	}
	allowed := [][]string{
		{"go", "test", "./..."},
		{"zig", "build", "client"},
		{"rm", "-rf", "build"},
		{"rm", "-rf", "./zig-cache"},
		{"cmd", "/c", "rd /s /q zig-out"},
		{"git", "status"},
		{"git", "commit", "-m", "format code and shutdown hook"},
		{"git", "push", "origin", "main"},
		{"git", "reset", "--soft", "HEAD~1"},
		{"git", "clean", "-n"},
		{"powershell", "-Command", "Remove-Item build\\out.txt"},
		{"echo", "rm -rf / is dangerous"},
	}
	for _, argv := range allowed {
		raw, _ := json.Marshal(map[string]any{"command": argv})
		if r := destructiveCommandRefusal("ctx_execute", string(raw)); r != "" {
			t.Errorf("blocked an ordinary command %q: %s", argv, r)
		}
	}
}

// The guard runs at dispatch: the tool itself is never called.
func TestDestructiveCommandIsNeverExecuted(t *testing.T) {
	reg := tools.NewRegistry()
	var ran atomic.Int32
	reg.MustRegister(tools.Tool{Name: "ctx_execute", Description: "run a command", Schema: `{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"}}}}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			ran.Add(1)
			return tools.Result{Text: "ran"}, nil
		}})
	reg.MarkAlwaysOn("ctx_execute")
	prov := &stubProvider{name: "t", scripts: [][]llm.Delta{
		callStep(tcall("c1", "ctx_execute", `{"command":["rm","-rf","/"]}`)),
		say("Nie uruchomię tego."),
	}}
	l, err := NewLoop(LoopConfig{Provider: prov, Registry: reg, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	runTurnText(t, l, "wyczyść dysk")
	if ran.Load() != 0 {
		t.Fatal("a destructive command was executed")
	}
	if !requestContains(prov.reqs[1], "blocked: destructive command") {
		t.Fatal("the model was not told why")
	}
}
