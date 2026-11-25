package webgui

import (
	"context"
	"os"
	"path/filepath"
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
