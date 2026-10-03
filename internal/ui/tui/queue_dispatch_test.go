package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

type queueRunAgent struct {
	calls []string
	err   error
	onRun func()
}

func (a *queueRunAgent) Name() string { return "queued-test" }
func (a *queueRunAgent) Run(ctx context.Context, prompt string) (<-chan agent.Event, error) {
	a.calls = append(a.calls, prompt)
	if a.onRun != nil {
		a.onRun()
	}
	if a.err != nil {
		return nil, a.err
	}
	ch := make(chan agent.Event)
	close(ch)
	return ch, nil
}

func queueFixture(t *testing.T, ag agent.Agent, sessionID string) (Model, *session.Store, session.QueuedTask) {
	t.Helper()
	home, data := t.TempDir(), t.TempDir()
	store, err := session.OpenStore(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if sessionID != "" {
		sess, e := store.Create(home, "queued-test", sessionID)
		if e != nil {
			t.Fatal(e)
		}
		sessionID = sess.ID
	}
	row, err := store.EnqueueTask(context.Background(), home, sessionID, "queued task")
	if err != nil {
		t.Fatal(err)
	}
	m := New(Options{Home: home, DataDir: data, SessionID: sessionID, SessionStore: store, Agent: ag, NoColor: true})
	next, _ := m.openQueueMenu()
	return next.(Model), store, row
}

func requireQueuedRow(t *testing.T, store *session.Store, home string, row session.QueuedTask) {
	t.Helper()
	rows, err := store.ListQueuedTasks(context.Background(), home)
	if err != nil || len(rows) != 1 || rows[0].ID != row.ID {
		t.Fatalf("queued row lost: %+v / %v", rows, err)
	}
}

func TestQueuedTaskWaitsForRunAcceptance(t *testing.T) {
	ag := &queueRunAgent{}
	m, store, row := queueFixture(t, ag, "current")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	requireQueuedRow(t, store, m.home, row)
	if cmd == nil || !m.busy || len(ag.calls) != 0 {
		t.Fatal("queued run must be deferred and keep composer reserved")
	}
	started := cmd().(runStartMsg)
	if started.err != nil || len(ag.calls) != 1 || ag.calls[0] != row.Prompt {
		t.Fatalf("start=%+v calls=%v", started, ag.calls)
	}
	rows, err := store.ListQueuedTasks(context.Background(), m.home)
	if err != nil || len(rows) != 0 {
		t.Fatalf("accepted row not removed: %+v / %v", rows, err)
	}
}

func TestQueuedTaskRetainedWhenRunDoesNotStart(t *testing.T) {
	for _, scenario := range []string{"no agent", "Run rejection", "Stop before Run"} {
		t.Run(scenario, func(t *testing.T) {
			ag := &queueRunAgent{err: errors.New("provider rejected")}
			var target agent.Agent = ag
			if scenario == "no agent" {
				target = nil
			}
			m, store, row := queueFixture(t, target, "current")
			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(Model)
			if scenario == "Stop before Run" {
				next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
				m = next.(Model)
			}
			if cmd != nil {
				next, _ = m.Update(cmd())
				m = next.(Model)
			}
			requireQueuedRow(t, store, m.home, row)
			if scenario != "Run rejection" && len(ag.calls) != 0 {
				t.Fatal("unexpected model invocation")
			}
			if m.input.Value() != "" {
				t.Fatalf("durable queued prompt copied into composer: %q", m.input.Value())
			}
		})
	}
}

func TestQueuedTaskPreservesUnsentDraftAndAttachments(t *testing.T) {
	ag := &queueRunAgent{}
	m, store, row := queueFixture(t, ag, "current")
	drafts, err := OpenDraftRecovery(t.TempDir(), m.home)
	if err != nil {
		t.Fatal(err)
	}
	m.drafts = drafts
	t.Cleanup(func() { _ = drafts.Flush() })
	m.input.SetValue("unsent composer draft")
	m.pendingAttachments = []string{"unsent.png"}
	drafts.Update(DraftSnapshot{SessionID: m.sessionID, Text: m.input.Value(), Attachments: m.pendingAttachments})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if got := drafts.Snapshot(); got.Text != "unsent composer draft" || !reflect.DeepEqual(got.Attachments, []string{"unsent.png"}) {
		t.Fatalf("queued send replaced recovery draft: %+v", got)
	}
	started := cmd().(runStartMsg)
	if started.err != nil || started.attachmentsSent || len(ag.calls) != 1 || ag.calls[0] != row.Prompt {
		t.Fatalf("queued prompt consumed composer attachments: %+v / %v", started, ag.calls)
	}
	next, _ = m.Update(started)
	m = next.(Model)
	if m.input.Value() != "unsent composer draft" || !reflect.DeepEqual(m.pendingAttachments, []string{"unsent.png"}) {
		t.Fatal("queued send changed composer draft")
	}
	rows, err := store.ListQueuedTasks(context.Background(), m.home)
	if err != nil || len(rows) != 0 {
		t.Fatalf("accepted queue row remains: %+v / %v", rows, err)
	}
}

func TestQueuedTaskResumesItsOriginalSession(t *testing.T) {
	ag := &queueRunAgent{}
	m, store, row := queueFixture(t, ag, "current")
	old, err := store.Create(m.home, "queued-test", "original conversation")
	if err != nil {
		t.Fatal(err)
	}
	if err = session.NewWriter(store, old.ID).AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: "old question"}); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteQueuedTask(context.Background(), m.home, row.ID); err != nil {
		t.Fatal(err)
	}
	row, err = store.EnqueueTask(context.Background(), m.home, old.ID, row.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	m = m.reloadQueue()
	resumed := ""
	m.resumeSession = func(_ context.Context, id string, messages []llm.Message, _ []string) error {
		resumed = id
		if len(messages) == 0 || messages[0].Content != "old question" {
			t.Fatalf("wrong resume history: %+v", messages)
		}
		return nil
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	requireQueuedRow(t, store, m.home, row)
	if len(ag.calls) != 0 || m.resumeContext == nil {
		t.Fatal("queued prompt ran before its original session loaded")
	}
	next, cmd = m.Update(cmd())
	m = next.(Model)
	if resumed != old.ID || m.sessionID != old.ID || cmd == nil {
		t.Fatalf("session=%q resumed=%q cmd=%v", m.sessionID, resumed, cmd != nil)
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if len(ag.calls) != 1 || ag.calls[0] != row.Prompt || !strings.Contains(m.completedLines(), "old question") {
		t.Fatalf("wrong queued continuation: %+v / %q", ag.calls, m.completedLines())
	}
}

func TestStopWhileQueuedSessionLoadsKeepsTheRow(t *testing.T) {
	ag := &queueRunAgent{}
	m, store, row := queueFixture(t, ag, "current")
	currentID := m.sessionID
	old, err := store.Create(m.home, "queued-test", "original")
	if err != nil {
		t.Fatal(err)
	}
	if err = session.NewWriter(store, old.ID).AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: "old question"}); err != nil {
		t.Fatal(err)
	}
	store.DeleteQueuedTask(context.Background(), m.home, row.ID)
	row, err = store.EnqueueTask(context.Background(), m.home, old.ID, row.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	m = m.reloadQueue()
	m.resumeSession = func(context.Context, string, []llm.Message, []string) error {
		t.Fatal("cancelled queue changed runtime")
		return nil
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	loaded := cmd()
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	next, _ = m.Update(loaded)
	m = next.(Model)
	requireQueuedRow(t, store, m.home, row)
	if len(ag.calls) != 0 || m.busy || m.sessionID != currentID {
		t.Fatal("Stop dispatched the queued prompt")
	}
}

func TestQueuedTaskDequeueDoesNotRemoveAnotherItem(t *testing.T) {
	ag := &queueRunAgent{}
	m, store, selected := queueFixture(t, ag, "current")
	other, err := store.EnqueueTask(context.Background(), m.home, m.sessionID, "another queued task")
	if err != nil {
		t.Fatal(err)
	}
	m = m.reloadQueue()
	next, cmd := m.runQueuedTask()
	m = next.(Model)
	started := cmd().(runStartMsg)
	if started.err != nil {
		t.Fatal(started.err)
	}
	rows, err := store.ListQueuedTasks(context.Background(), m.home)
	if err != nil || len(rows) != 1 || rows[0].ID != other.ID || len(ag.calls) != 1 || ag.calls[0] != selected.Prompt {
		t.Fatalf("wrong queued task consumed: %+v / %v, calls=%v", rows, err, ag.calls)
	}
}

func TestQueuedTaskReportsFailedDequeueWithoutRepeatingRun(t *testing.T) {
	ag := &queueRunAgent{}
	m, store, row := queueFixture(t, ag, "current")
	ag.onRun = func() { _ = store.Close() }
	next, cmd := m.runQueuedTask()
	m = next.(Model)
	started := cmd().(runStartMsg)
	if started.err != nil || started.queueWarning == nil {
		t.Fatalf("dequeue failure not reported separately from accepted run: %+v", started)
	}
	next, _ = m.Update(started)
	m = next.(Model)
	if !strings.Contains(m.completedLines(), started.queueWarning.Error()) {
		t.Fatal("dequeue failure not visible")
	}
	next, _ = m.Update(runEndMsg{})
	m = next.(Model)
	reopened, err := session.OpenStore(m.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	requireQueuedRow(t, reopened, m.home, row)
	if len(ag.calls) != 1 || m.busy {
		t.Fatal("accepted queue item repeated after deletion failure")
	}
}

func TestQueuedTaskRejectedSessionPreparationKeepsQueueAndDraft(t *testing.T) {
	ag := &queueRunAgent{}
	m, store, row := queueFixture(t, ag, "current")
	old, err := store.Create(m.home, "queued-test", "original")
	if err != nil {
		t.Fatal(err)
	}
	if err = session.NewWriter(store, old.ID).AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: "old question"}); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteQueuedTask(context.Background(), m.home, row.ID); err != nil {
		t.Fatal(err)
	}
	row, err = store.EnqueueTask(context.Background(), m.home, old.ID, row.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	m = m.reloadQueue()
	m.input.SetValue("unsent draft")
	m.prepareResume = func(context.Context, string) error { return errors.New("cannot prepare session") }
	next, cmd := m.runQueuedTask()
	m = next.(Model)
	next, cmd = m.Update(cmd())
	m = next.(Model)
	requireQueuedRow(t, store, m.home, row)
	if cmd != nil || len(ag.calls) != 0 || m.busy || m.input.Value() != "unsent draft" {
		t.Fatal("rejected session preparation lost work or invoked Run")
	}
}

func TestQueuedTaskCannotOverlapBusyRun(t *testing.T) {
	ag := &queueRunAgent{}
	m, store, row := queueFixture(t, ag, "current")
	m.busy = true
	next, _ := m.runQueuedTask()
	m = next.(Model)
	requireQueuedRow(t, store, m.home, row)
	if !m.busy || m.statusOverride == "" || m.mode != modeMenu || len(ag.calls) != 0 {
		t.Fatal("busy queued action silently lost or overlapped active run")
	}
}
