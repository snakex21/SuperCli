package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func TestResumeContinuesOriginalSessionAndUsage(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	old, _ := store.Create(home, "echo", "history")
	fresh, _ := store.Create(home, "echo", "launch")
	writer := session.NewWriter(store, old.ID)
	if err = writer.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "old question"}); err != nil {
		t.Fatal(err)
	}
	if err = writer.AppendMessage(ctx, llm.Message{Role: llm.RoleAssistant, Content: "old answer"}); err != nil {
		t.Fatal(err)
	}
	provider, _ := llm.NewEcho("echo")
	loop, err := agent.NewLoop(agent.LoopConfig{Provider: provider, Registry: tools.NewRegistry(), Writer: session.NewWriter(store, fresh.ID)})
	if err != nil {
		t.Fatal(err)
	}
	m := New(Options{Home: home, SessionID: fresh.ID, SessionStore: store, Agent: loop, LLM: provider, NoColor: true,
		ResumeSession: func(ctx context.Context, id string, messages []llm.Message, discovered []string) error {
			return loop.ResumeConversation(ctx, session.NewWriter(store, id), messages, discovered)
		}})
	next, cmd := m.resumeConversation(old.ID)
	next, _ = next.(Model).Update(cmd())
	m = next.(Model)
	if m.sessionID != old.ID || loop.SessionID() != old.ID {
		t.Fatal("writer/UI still use launch session")
	}
	next, cmd = m.startPrompt("continue this conversation")
	m = next.(Model)
	started := cmd().(runStartMsg)
	if started.err != nil {
		t.Fatal(started.err)
	}
	next, _ = m.Update(started)
	m = next.(Model)
	for ev := range started.ch {
		next, _ = m.Update(runEventMsg{ev: ev})
		m = next.(Model)
	}
	next, _ = m.Update(runEndMsg{})
	m = next.(Model)
	rows, _ := store.ReadMessages(ctx, old.ID)
	if len(rows) < 4 || rows[2].Content != "continue this conversation" {
		t.Fatalf("continuation missing: %#v", rows)
	}
	untouched, _ := store.ReadMessages(ctx, fresh.ID)
	for _, row := range untouched {
		if row.Role != "system" {
			t.Fatal("continuation leaked to launch session")
		}
	}
	sink := m.sessionUsageSink()
	m.sessionID = fresh.ID
	sink(llm.CallStat{TokensIn: 101, TokensOut: 9, Model: "echo", Purpose: "main"})
	usage, _ := store.ReadUsage(ctx, old.ID)
	other, _ := store.ReadUsage(ctx, fresh.ID)
	if len(usage) != 1 || usage[0].Input != 101 || len(other) != 0 {
		t.Fatal("late usage attributed to wrong session")
	}
	// GUI reads the same canonical rows/model context.
	contextMessages, err := store.ReadModelContext(ctx, old.ID)
	if err != nil || len(contextMessages) < 4 {
		t.Fatalf("GUI cannot resume continuation: %v", err)
	}
}

func TestCancelledResumeCannotChangeAgentOrNewDraft(t *testing.T) {
	store, _ := session.OpenStore(t.TempDir())
	defer store.Close()
	old, _ := store.Create(t.TempDir(), "echo", "old")
	session.NewWriter(store, old.ID).AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: "past"})
	committed := false
	m := New(Options{SessionStore: store, SessionID: "current", ResumeSession: func(context.Context, string, []llm.Message, []string) error { committed = true; return nil }})
	next, cmd := m.resumeConversation(old.ID)
	m = next.(Model)
	result := cmd()
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	m.input.SetValue("new unsent message")
	next, _ = m.Update(result)
	m = next.(Model)
	if committed || m.resumeContext != nil || m.sessionID != "current" || m.input.Value() != "new unsent message" {
		t.Fatal("cancelled resume committed")
	}
}

func TestDraftRecoveryCoalescesAndRestoresAttachments(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	d, err := OpenDraftRecovery(dir, home)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Flush()
	image := filepath.Join(dir, "screenshot.png")
	os.WriteFile(image, []byte("fixture"), 0600)
	for i := 0; i < 30; i++ {
		d.Update(DraftSnapshot{SessionID: "saved", Text: strings.Repeat("x", i+1), Attachments: []string{image}})
	}
	if _, err = os.Stat(d.path); !os.IsNotExist(err) {
		t.Fatal("wrote synchronously on each edit")
	}
	if err = d.Flush(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDraftRecovery(dir, home)
	if err != nil {
		t.Fatal(err)
	}
	m := New(Options{Home: home, DataDir: dir, SessionID: "launch", DraftRecovery: reopened})
	if m.input.Value() != strings.Repeat("x", 30) || !reflect.DeepEqual(m.pendingAttachments, []string{image}) {
		t.Fatal("draft/attachment lost")
	}
	next, _ := m.Update(runStartMsg{err: errors.New("provider failed"), draft: "submitted text"})
	if next.(Model).input.Value() != strings.Repeat("x", 30) {
		t.Fatal("failed run overwrote newer draft")
	}
	reopened.Update(DraftSnapshot{SessionID: "launch"})
	if err = reopened.Flush(); err != nil {
		t.Fatal(err)
	}
	empty, err := OpenDraftRecovery(dir, home)
	if err != nil || empty.Snapshot().Text != "" {
		t.Fatal("sent/cleared draft came back")
	}
}

func TestClipboardImageCompletionPreservesDraft(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "screen.png")
	os.WriteFile(path, []byte("fixture"), 0600)
	m := New(Options{Home: home})
	m.input.SetValue("what is on this screenshot?")
	m.attachmentPickerOpen = true
	next, _ := m.Update(clipboardImageMsg{path: path})
	m = next.(Model)
	if m.attachmentPickerOpen || m.input.Value() != "what is on this screenshot?" || !reflect.DeepEqual(m.pendingAttachments, []string{path}) {
		t.Fatal("clipboard lost draft")
	}
}

func TestDoneWaitsForStreamClose(t *testing.T) {
	m := New(Options{})
	ch := make(chan agent.Event, 1)
	m.eventCh = ch
	m.busy = true
	next, cmd := m.Update(runEventMsg{ev: agent.DoneEvent{}})
	if !next.(Model).busy || cmd == nil {
		t.Fatal("finished before persistence")
	}
	close(ch)
	next, _ = next.(Model).Update(cmd())
	if next.(Model).busy {
		t.Fatal("did not finish after stream closed")
	}
}

func TestCancelWaitsForOldRunAndKeepsNextDraft(t *testing.T) {
	m := New(Options{})
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel.Arm(cancelRun, cancel)
	ch := make(chan agent.Event)
	m.eventCh = ch
	m.busy = true
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if ctx.Err() == nil || !m.cancelling || !m.busy {
		t.Fatal("released run before cleanup")
	}
	m.input.SetValue("next task")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.input.Value() != "next task" || !m.busy {
		t.Fatal("queued draft into cancelled run")
	}
	close(ch)
	next, _ = m.Update(m.waitForNextEvent()())
	m = next.(Model)
	if m.busy || m.cancelling || m.input.Value() != "next task" {
		t.Fatal("did not preserve draft through cleanup")
	}
}
