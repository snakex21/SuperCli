// Tests for the idle-autosaver split: deterministic facts are
// saved immediately, the model-backed summary is batched and
// cancelable, and the exit path never calls the model.
package app

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/memory"
)

// countingProvider is a stub llm.Provider that counts Complete
// calls and returns a fixed summary. The name must not contain
// "echo" (usableSummaryProvider would reject it).
type countingProvider struct {
	calls atomic.Int32
	reply string
	fail  bool
}

func (p *countingProvider) Name() string { return "stub-small" }

func (p *countingProvider) Complete(ctx context.Context, msgs []llm.Message, tools []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls.Add(1)
	if p.fail || ctx.Err() != nil {
		return nil, context.Canceled
	}
	ch := make(chan llm.Delta, 1)
	ch <- llm.Delta{Content: p.reply}
	close(ch)
	return ch, nil
}

func newMemFixture(t *testing.T) (*memory.AutoSaver, *memory.Store, *memory.Store) {
	t.Helper()
	project, err := memory.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open project store: %v", err)
	}
	t.Cleanup(func() { _ = project.Close() })
	global, err := memory.OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open global store: %v", err)
	}
	t.Cleanup(func() { _ = global.Close() })
	return &memory.AutoSaver{Project: project, Global: global, ProjectPath: t.TempDir()}, project, global
}

func loopWithMessages(msgs ...llm.Message) *agent.Loop {
	return &agent.Loop{Messages: msgs}
}

// TestSaveDeterministicMemoryFacts_NoModel: the immediate per-turn
// hook persists user declarations without any provider at all.
func TestSaveDeterministicMemoryFacts_NoModel(t *testing.T) {
	saver, _, global := newMemFixture(t)
	loop := loopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: "cześć, nazywam się Maks"},
		llm.Message{Role: llm.RoleAssistant, Content: "Cześć Maks!"},
	)
	prog := &memProgress{}
	saveDeterministicMemoryFacts(saver, loop, prog)
	prefs, err := global.Recent(memory.ScopePreference, 10)
	if err != nil || len(prefs) == 0 {
		t.Fatalf("deterministic fact not saved: prefs=%v err=%v", prefs, err)
	}
	if prog.factsCovered != 2 {
		t.Errorf("factsCovered = %d, want 2", prog.factsCovered)
	}
}

// TestIncrementalMemorySave_BatchesBacklog: two turns accumulated
// before the idle window are summarized with ONE model call.
func TestIncrementalMemorySave_BatchesBacklog(t *testing.T) {
	saver, project, _ := newMemFixture(t)
	loop := loopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: "first question about parsers"},
		llm.Message{Role: llm.RoleAssistant, Content: "first answer"},
		llm.Message{Role: llm.RoleUser, Content: "second question about caches"},
		llm.Message{Role: llm.RoleAssistant, Content: "second answer"},
	)
	prog := &memProgress{}
	p := &countingProvider{reply: "Worked on parsers and caches."}
	incrementalMemorySave(context.Background(), saver, loop, prog, p)
	if got := p.calls.Load(); got != 1 {
		t.Fatalf("summary calls = %d, want 1 (backlog batched)", got)
	}
	if prog.covered != 4 {
		t.Errorf("covered = %d, want 4", prog.covered)
	}
	logs, err := project.Recent(memory.ScopeTaskLog, 5)
	if err != nil || len(logs) != 1 {
		t.Fatalf("task logs = %v, err %v, want exactly 1", logs, err)
	}
}

// TestIncrementalMemorySave_CanceledKeepsBacklog: a canceled
// context (user sent a new prompt) must not advance coverage —
// the fragment is retried at the next idle window.
func TestIncrementalMemorySave_CanceledKeepsBacklog(t *testing.T) {
	saver, project, _ := newMemFixture(t)
	loop := loopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: "a question that must not get lost"},
		llm.Message{Role: llm.RoleAssistant, Content: "an answer"},
	)
	prog := &memProgress{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &countingProvider{reply: "should not be stored"}
	incrementalMemorySave(ctx, saver, loop, prog, p)
	if prog.covered != 0 {
		t.Fatalf("covered = %d after canceled save, want 0 (retry later)", prog.covered)
	}
	if logs, _ := project.Recent(memory.ScopeTaskLog, 5); len(logs) != 0 {
		t.Fatalf("task log stored from a canceled call: %v", logs)
	}
	// Retry at the next idle window succeeds and covers the backlog.
	incrementalMemorySave(context.Background(), saver, loop, prog, p)
	if prog.covered != 2 {
		t.Errorf("covered after retry = %d, want 2", prog.covered)
	}
}

// TestFinalizeMemorySession_NoModelCall: the exit path stores the
// uncovered tail RAW — verbatim, zero inference — and the next
// startup's idle pass turns it into a task-log entry.
func TestFinalizeMemorySession_NoModelCall(t *testing.T) {
	saver, project, _ := newMemFixture(t)
	loop := loopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: "remember that I prefer tabs"},
		llm.Message{Role: llm.RoleAssistant, Content: "noted"},
	)
	prog := &memProgress{}
	// finalizeMemorySession takes no provider — the compiler already
	// guarantees no model call; assert the raw tail actually lands.
	finalizeMemorySession(saver, loop, prog)
	raws, err := project.Recent(memory.ScopeRawLog, 5)
	if err != nil || len(raws) != 1 {
		t.Fatalf("raw logs = %v, err %v, want exactly 1", raws, err)
	}
	if !strings.Contains(raws[0].Content, "prefer tabs") {
		t.Errorf("raw tail = %q, want the user's words verbatim", raws[0].Content)
	}
	if logs, _ := project.Recent(memory.ScopeTaskLog, 5); len(logs) != 0 {
		t.Fatalf("exit path stored a summarized task log: %v", logs)
	}
}

// TestFinalizeMemorySession_CoveredTailIsInstant: when the idle
// saver already covered everything, the exit stores nothing.
func TestFinalizeMemorySession_CoveredTailIsInstant(t *testing.T) {
	saver, project, _ := newMemFixture(t)
	loop := loopWithMessages(
		llm.Message{Role: llm.RoleUser, Content: "hello"},
		llm.Message{Role: llm.RoleAssistant, Content: "hi"},
	)
	prog := &memProgress{covered: 2, factsCovered: 2}
	finalizeMemorySession(saver, loop, prog)
	if raws, _ := project.Recent(memory.ScopeRawLog, 5); len(raws) != 0 {
		t.Fatalf("raw tail stored despite full coverage: %v", raws)
	}
}
