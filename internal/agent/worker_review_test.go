package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// reviewFixture: a coordinator whose workers can edit files (a create_file
// tool), on the synchronous path (no wake host) so each test reads the
// worker's notification directly.
func reviewFixture(t *testing.T, worker [][]llm.Delta) (statusTurnFixture, *atomic.Int32) {
	t.Helper()
	f := newStatusTurnFixture(t,
		[][]llm.Delta{callStep(tcall("t1", "task", `{"prompt":"fix the build"}`)), say("Delegated.")},
		worker)
	var writes atomic.Int32
	f.parent.registry.MustRegister(tools.Tool{
		Name: "create_file", Description: "create a file",
		Schema: `{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}`,
		Fn: func(_ context.Context, raw json.RawMessage) (tools.Result, error) {
			var args struct{ Path, Content string }
			_ = json.Unmarshal(raw, &args)
			if err := os.WriteFile(args.Path, []byte(args.Content), 0o600); err != nil {
				return tools.Result{Err: err}, nil
			}
			writes.Add(1)
			return tools.Result{Text: "Created " + args.Path, ChangedPaths: []string{args.Path}}, nil
		},
	})
	return f, &writes
}

func taskNotification(t *testing.T, f statusTurnFixture) string {
	t.Helper()
	for _, m := range f.parent.Messages {
		if m.Role == llm.RoleTool && m.Name == "task" {
			return m.Content
		}
	}
	t.Fatal("no task result")
	return ""
}

// A file-changing worker that says "done" without changing anything is
// marked for review, in its state and in the report the coordinator reads.
func TestWorkerDoneWithoutChangesNeedsReview(t *testing.T) {
	f, _ := reviewFixture(t, [][]llm.Delta{say("Fixed the build, all good.")})
	runTurnText(t, f.parent, "Napraw build.")
	w := f.task.Workers.List()[0]
	s := w.Snapshot()
	if s.Status != "done" || s.Review != ReviewNoChanges {
		t.Fatalf("status=%s review=%q", s.Status, s.Review)
	}
	if note := taskNotification(t, f); !strings.Contains(note, "<review>"+ReviewNoChanges+"</review>") {
		t.Fatalf("the coordinator was not told: %s", note)
	}
}

// After real changes, a configured verify command decides: red marks the
// worker for review with the command and exit code; green leaves it done.
func TestWorkerVerifyCommandAfterChanges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		exit   int
		review string
	}{
		{"red", 1, "review: verify command failed: zig build client (exit 1)"},
		{"green", 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, _ := json.Marshal(map[string]string{"path": filepath.Join(t.TempDir(), "a.zig"), "content": "const x = 1;"})
			f, writes := reviewFixture(t, [][]llm.Delta{
				callStep(tcall("w1", "create_file", string(target))),
				say("Done."),
			})
			var ran []string
			f.task.VerifyCommands = []string{"zig build client"}
			f.task.verifyRunner = func(_ context.Context, _ string, command string, _ time.Duration) (int, string) {
				ran = append(ran, command)
				return tc.exit, "src/main.zig:126:5: error: expected ';'"
			}
			runTurnText(t, f.parent, "Napraw build.")
			if writes.Load() != 1 || len(ran) != 1 {
				t.Fatalf("writes=%d verify runs=%v review=%q", writes.Load(), ran, f.task.Workers.List()[0].Snapshot().Review)
			}
			s := f.task.Workers.List()[0].Snapshot()
			if tc.review == "" && s.Review != "" {
				t.Fatalf("a green check still needs review: %q", s.Review)
			}
			if tc.review != "" && !strings.HasPrefix(s.Review, tc.review) {
				t.Fatalf("review = %q, want prefix %q", s.Review, tc.review)
			}
		})
	}
}

// Read-only workers (no edit tools) are never asked for changes.
func TestReadOnlyWorkerNeedsNoReview(t *testing.T) {
	f := newStatusTurnFixture(t,
		[][]llm.Delta{callStep(tcall("t1", "task", `{"prompt":"explain the build"}`)), say("Delegated.")},
		[][]llm.Delta{say("The build has two steps.")})
	runTurnText(t, f.parent, "Wyjaśnij build.")
	if s := f.task.Workers.List()[0].Snapshot(); s.Review != "" {
		t.Fatalf("read-only worker marked for review: %q", s.Review)
	}
}

// After a restart a worker exists only in the database (reconciled to
// "interrupted"); an automatic turn must still not resume it.
func TestWakeTurnRefusesResumingWorkerKnownOnlyFromDatabase(t *testing.T) {
	coordinator := &stubProvider{name: "c", scripts: [][]llm.Delta{
		callStep(tcall("s1", "send_message", `{"to":"worker-7","message":"Wznowienie: kontynuuj."}`)),
		say("worker-7 przerwał się przy restarcie; mogę go wznowić."),
	}}
	reg := tools.NewRegistry()
	writer := &sessionWriterWithID{id: "sess-restart"}
	parent, err := NewLoop(LoopConfig{Provider: coordinator, Registry: reg, Writer: writer, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	child := &stubProvider{name: "w", scripts: [][]llm.Delta{say("resumed")}}
	task, err := NewAgentTool(specs, parent, reg, child, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	store := newMemWorkerStore()
	_ = store.SaveWorker(context.Background(), WorkerRecord{SessionID: "sess-restart", ID: "worker-7", Agent: "code", Status: WorkerStatusInterrupted, Runs: 1})
	task.Workers.SetStore(store, []string{"worker-7"})
	reg.MustRegister(task.Spec())
	reg.MustRegister(NewSendMessageToolFor(task).Spec())
	reg.MarkAlwaysOn("task")
	reg.MarkAlwaysOn("send_message")
	parent.SetBackgroundWake(WakeConfig{Debounce: testWakeDebounce})
	parent.Messages = append(parent.Messages, llm.Message{Role: llm.RoleUser, Content: "Dodaj moby."}, llm.Message{Role: llm.RoleAssistant, Content: "Delegated."})
	parent.DeliverWorkerReport(context.Background(), "<task-notification><task-id>worker-8</task-id><status>done</status></task-notification>")
	events, err := parent.RunWake(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	refused := false
	for ev := range events {
		if r, ok := ev.(ToolResultEvent); ok && r.Err != nil && strings.Contains(r.Err.Error(), "automatic turn without a user message") {
			refused = true
		}
	}
	if !refused || atomic.LoadInt32(&child.calls) != 0 {
		t.Fatalf("a worker known only from the database was resumed by an automatic turn (refused=%v)", refused)
	}
}

// Deliberate decision: SuperCli keeps no record of an approved plan (plan
// mode is a prompt-only instruction; the goal tool tracks progress, not
// approval), so even a standing "resume them automatically" in the chat does
// not let an automatic turn resume a failed worker. Only the user does.
func TestStandingInstructionDoesNotAuthorizeAutomaticResume(t *testing.T) {
	f, _ := contractFixture(t, true,
		[][]llm.Delta{
			callStep(tcall("t1", "task", `{"prompt":"spider"}`)), say("Delegated."),
			callStep(tcall("s1", "send_message", `{"to":"worker-1","message":"continue"}`)), say("worker-1 failed."),
		},
		[][]llm.Delta{say("partial")})
	runTurnWithin(t, f.parent, "Dodaj pająka i wznawiaj workery automatycznie po każdym błędzie.", 3*time.Second)
	w := f.task.Workers.List()[0]
	waitWorker(t, w, "first run should end", func(s Snapshot) bool { return s.Status == "done" })
	w.setState(func(w *Worker) { w.Status, w.LastError = "failed", "stream error" })
	f.parent.DeliverWorkerReport(context.Background(), "<task-notification><task-id>worker-1</task-id><status>failed</status></task-notification>")
	events, err := f.parent.RunWake(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	if s := w.Snapshot(); s.Runs != 1 {
		t.Fatalf("an automatic turn resumed the worker on a standing instruction: runs=%d", s.Runs)
	}
}
