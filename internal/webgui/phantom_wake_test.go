package webgui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

type countingAnswerProvider struct{ calls atomic.Int32 }

func (*countingAnswerProvider) Name() string { return "count-fixture" }

func (p *countingAnswerProvider) Complete(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls.Add(1)
	return workerSteeringWireDeltas(llm.Delta{Content: "Answer with every report seen.", FinishReason: "stop"}), nil
}

// Audited phantom automatic turns: a detached loop saved a worker report, a
// user turn loaded it (and answered it), and an automatic turn fired anyway
// after the answer, with nothing new. Every report in the last request means
// no wake-up.
func TestNoWakeWhenTheLastTurnAlreadySawTheReports(t *testing.T) {
	srv := newTestServer(t, false)
	eng := srv.eng
	eng.setHome(t.TempDir())
	if err := os.WriteFile(filepath.Join(eng.DataDir(), "config.toml"), []byte("orchestrator = true\npreflight_repo = false\nbackground_wake_debounce_ms = 400\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &countingAnswerProvider{}
	eng.mu.Lock()
	eng.prov = p
	eng.mu.Unlock()
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(eng.Home(), "count-fixture", "phantom wake")
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := session.FromMessage(llm.Message{Role: llm.RoleUser, Content: "zacznij"})
	_ = store.AppendMessage(context.Background(), sess.ID, enc)

	// The loop of an earlier turn, now detached, receives a worker report.
	earlier, err := agent.NewLoop(agent.LoopConfig{Provider: p, Registry: tools.NewRegistry(), Writer: session.NewWriter(store, sess.ID), MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	eng.backgroundFor(sess.ID, eng.Home()).attach(earlier, true)
	earlier.DeliverWorkerReport(context.Background(), "<task-notification>\n<task-id>worker-1</task-id>\n<agent>code</agent>\n<status>completed</status>\n<summary>done</summary>\n</task-notification>")

	// The user writes before the wake-up debounce elapses: this turn loads
	// the saved report and answers it.
	if err := eng.runStream(context.Background(), "co z workerem?", sess.ID, "", func(wireEvent) {}); err != nil {
		t.Fatal(err)
	}
	after := p.calls.Load()
	time.Sleep(1200 * time.Millisecond)
	if got := p.calls.Load(); got != after {
		t.Fatalf("an automatic turn ran after the report was already answered (%d extra calls)", got-after)
	}
}

type recordingAnswerProvider struct {
	mu   sync.Mutex
	reqs [][]llm.Message
}

func (*recordingAnswerProvider) Name() string { return "record-fixture" }

func (p *recordingAnswerProvider) Complete(_ context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.mu.Lock()
	p.reqs = append(p.reqs, append([]llm.Message(nil), msgs...))
	p.mu.Unlock()
	return workerSteeringWireDeltas(llm.Delta{Content: "Handled.", FinishReason: "stop"}), nil
}

// Audited missed reports: a detached loop saved worker reports while a newer
// run was writing its answer, so they sit above that answer. The automatic
// turn that follows names them as new.
func TestReportSavedDuringAnotherRunIsNamedInTheNextWake(t *testing.T) {
	srv := newTestServer(t, false)
	eng := srv.eng
	eng.setHome(t.TempDir())
	if err := os.WriteFile(filepath.Join(eng.DataDir(), "config.toml"), []byte("orchestrator = true\npreflight_repo = false\nbackground_wake_debounce_ms = 60000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &recordingAnswerProvider{}
	eng.mu.Lock()
	eng.prov = p
	eng.mu.Unlock()
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(eng.Home(), "record-fixture", "missed reports")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	ctx := context.Background()
	_ = writer.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "zacznij"})
	earlier, err := agent.NewLoop(agent.LoopConfig{Provider: p, Registry: tools.NewRegistry(), Writer: session.NewWriter(store, sess.ID), MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	bg := eng.backgroundFor(sess.ID, eng.Home())
	bg.attach(earlier, true)
	// A newer run is writing its answer; the earlier loop saves the reports.
	release, err := eng.claimSessionRun(ctx, sess.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"worker-393", "worker-392"} {
		earlier.DeliverWorkerReport(ctx, "<task-notification>\n<task-id>"+id+"</task-id>\n<agent>code</agent>\n<status>completed</status>\n<summary>"+id+" done</summary>\n</task-notification>")
	}
	_ = writer.AppendMessage(ctx, llm.Message{Role: llm.RoleAssistant, Content: "Nic nowego, czekam."})
	release()
	eng.runBackgroundWake(sess.ID, eng.Home(), agent.WakeRequest{})
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.reqs) == 0 {
		t.Fatal("no automatic turn ran")
	}
	last := p.reqs[len(p.reqs)-1]
	var text string
	for _, m := range last {
		text += m.Content + "\n" + m.TextOnly().Content + "\n"
	}
	for _, want := range []string{"New worker reports since your last reply", "worker-393", "worker-392"} {
		if !strings.Contains(text, want) {
			t.Fatalf("automatic turn does not name the unread reports (%q missing)", want)
		}
	}
}
