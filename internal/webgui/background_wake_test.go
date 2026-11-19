package webgui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
)

// wakeGUIProvider: the coordinator delegates one async worker and ends its
// turn; the worker answers only after `release`; the automatic turn must see
// the persisted report without any new human message.
type wakeGUIProvider struct {
	parentCalls, workerCalls atomic.Int32
	release                  chan struct{}
	userTurns                atomic.Int32
	mu                       sync.Mutex
	wakeErr                  error
	woke                     chan struct{}
	wokeOnce                 sync.Once
}

func (*wakeGUIProvider) Name() string { return "gui-wake-fixture" }

func (p *wakeGUIProvider) Complete(ctx context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	parent := false
	for _, def := range defs {
		parent = parent || def.Name == "task"
	}
	if !parent {
		p.workerCalls.Add(1)
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return workerSteeringWireDeltas(llm.Delta{Content: "GUI_LATE_REPORT: fixture reviewed", FinishReason: "stop"}), nil
	}
	switch p.parentCalls.Add(1) {
	case 1:
		return workerSteeringWireCall("spawn-bg", "task", map[string]any{"agent": "explore", "prompt": "Review the fixture in the background.", "async": true})
	case 2:
		return workerSteeringWireDeltas(llm.Delta{Content: "Started a background review.", FinishReason: "stop"}), nil
	default:
		humans, found := 0, false
		for _, m := range messages {
			if llm.IsConversationUserTurn(m) {
				humans++
			}
			found = found || strings.Contains(m.Content, "GUI_LATE_REPORT")
		}
		p.mu.Lock()
		if !found {
			p.wakeErr = errors.New("automatic turn without the worker report")
		} else if humans != 1 {
			p.wakeErr = errors.New("automatic turn added a human message")
		}
		p.mu.Unlock()
		p.wokeOnce.Do(func() { close(p.woke) })
		return workerSteeringWireDeltas(llm.Delta{Content: "Review done: GUI_LATE_REPORT applied.", FinishReason: "stop"}), nil
	}
}

func TestGUIBackgroundWorkerWakesIdleConversation(t *testing.T) {
	srv := newTestServer(t, false)
	srv.eng.setHome(t.TempDir())
	if err := os.WriteFile(filepath.Join(srv.eng.DataDir(), "config.toml"), []byte("orchestrator = true\npreflight_repo = false\nbackground_wake_debounce_ms = 30\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &wakeGUIProvider{release: make(chan struct{}), woke: make(chan struct{})}
	srv.eng.mu.Lock()
	srv.eng.prov = provider
	srv.eng.mu.Unlock()
	defer func() {
		select {
		case <-provider.release:
		default:
			close(provider.release)
		}
	}()

	sid := ""
	done := make(chan error, 1)
	go func() {
		done <- srv.eng.runStream(context.Background(), "Delegate a background review.", "", "", func(ev wireEvent) {
			if ev.Type == "session" {
				sid = ev.SessionID
			}
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("GUI turn blocked on its background worker")
	}
	if sid == "" || provider.workerCalls.Load() == 0 {
		t.Fatalf("sid=%q worker calls=%d", sid, provider.workerCalls.Load())
	}
	if counts := srv.eng.BackgroundAgentCounts(); counts[sid] != 1 {
		t.Fatalf("running background agents = %v", counts)
	}
	sub, snapshot, unsubscribe := srv.eng.syncHub.subscribe(sid, "observer")
	defer unsubscribe()
	if snapshot.Background[sid] != 1 {
		t.Fatalf("snapshot background counts = %v", snapshot.Background)
	}
	close(provider.release)

	var sawWorker, sawAuto, sawActivity, sawFinished, sawZero bool
	deadline := time.After(15 * time.Second)
	for !(sawFinished && sawZero) {
		select {
		case frame, ok := <-sub.events:
			if !ok {
				t.Fatal("sync closed")
			}
			switch frame.Type {
			case "background_event":
				sawWorker = sawWorker || (frame.Event != nil && (frame.Event.Type == "worker" || frame.Event.Type == "worker_progress"))
			case "background_agents":
				sawZero = sawZero || (frame.SessionID == sid && frame.Count == 0)
			case "run_started":
				if frame.SessionID == sid {
					sawAuto = frame.Auto
				}
			case "run_event":
				if frame.Event != nil && frame.Event.Type == "session_activity" && frame.Event.UserSeq > 0 {
					sawActivity = true
				}
			case "run_finished":
				sawFinished = sawFinished || frame.SessionID == sid
			}
		case <-deadline:
			t.Fatalf("worker=%v auto=%v activity=%v finished=%v zero=%v parent=%d", sawWorker, sawAuto, sawActivity, sawFinished, sawZero, provider.parentCalls.Load())
		}
	}
	waitCompletionTest(t, provider.woke, "automatic turn")
	provider.mu.Lock()
	wakeErr := provider.wakeErr
	provider.mu.Unlock()
	if wakeErr != nil {
		t.Fatal(wakeErr)
	}
	if !sawWorker || !sawAuto || !sawActivity {
		t.Fatalf("worker=%v auto=%v activity=%v", sawWorker, sawAuto, sawActivity)
	}
	if provider.parentCalls.Load() != 3 {
		t.Fatalf("parent calls = %d", provider.parentCalls.Load())
	}
	// Persisted: the reloaded conversation holds the report and the answer,
	// and no fabricated user message.
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	history, err := store.ReadModelContext(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	var report, answer bool
	humans := 0
	for _, m := range history {
		report = report || (m.Name == llm.WorkerNotificationName && strings.Contains(m.Content, "GUI_LATE_REPORT"))
		answer = answer || (m.Role == llm.RoleAssistant && strings.Contains(m.TextOnly().Content+m.Content, "applied"))
		if llm.IsConversationUserTurn(m) {
			humans++
		}
	}
	if !report || !answer || humans != 1 {
		t.Fatalf("reloaded session: report=%v answer=%v humans=%d", report, answer, humans)
	}
}

// A second user message while the worker runs: the new turn is not blocked,
// the worker's events keep reaching the browser (no frozen card) and its
// report wakes the conversation after that user turn.
func TestGUIUserMessageDoesNotFreezeBackgroundWorker(t *testing.T) {
	srv := newTestServer(t, false)
	srv.eng.setHome(t.TempDir())
	if err := os.WriteFile(filepath.Join(srv.eng.DataDir(), "config.toml"), []byte("orchestrator = true\npreflight_repo = false\nbackground_wake_debounce_ms = 30\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &wakeGUIProvider{release: make(chan struct{}), woke: make(chan struct{})}
	srv.eng.mu.Lock()
	srv.eng.prov = provider
	srv.eng.mu.Unlock()
	released := false
	defer func() {
		if !released {
			close(provider.release)
		}
	}()
	sid := ""
	if err := srv.eng.runStream(context.Background(), "Delegate a background review.", "", "", func(ev wireEvent) {
		if ev.Type == "session" {
			sid = ev.SessionID
		}
	}); err != nil {
		t.Fatal(err)
	}
	sub, _, unsubscribe := srv.eng.syncHub.subscribe(sid, "observer")
	defer unsubscribe()
	// Second user turn: parent call 3 answers it. The worker finishes during it.
	var during atomic.Bool
	second := make(chan error, 1)
	go func() {
		second <- srv.eng.runStream(context.Background(), "SECOND question while it works", sid, "", func(ev wireEvent) {
			if ev.Type == "session_activity" && !released {
				released = true
				during.Store(true)
				close(provider.release)
			}
		})
	}()
	sawWorker := false
	deadline := time.After(15 * time.Second)
	for !sawWorker {
		select {
		case frame := <-sub.events:
			if frame.Type == "background_event" && frame.Event != nil && frame.Event.Type == "worker" {
				sawWorker = true
			}
		case <-deadline:
			t.Fatal("worker events of the earlier turn stopped reaching the browser")
		}
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if !during.Load() {
		t.Fatal("worker was not released during the second turn")
	}
	if provider.parentCalls.Load() < 3 {
		t.Fatalf("parent calls = %d", provider.parentCalls.Load())
	}
}

// A worker saved as running by an earlier process is reported with a
// definite, resumable state (never "unconfirmed") and the coordinator is told
// at the start of its next turn.
func TestGUISavedOrphanWorkerIsInterruptedAndResumable(t *testing.T) {
	srv := newTestServer(t, false)
	home := t.TempDir()
	srv.eng.setHome(home)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(home, "m", "t")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.SaveWorker(ctx, session.WorkerRow{SessionID: sess.ID, ID: "worker-41", Agent: "code", Description: "build", Status: "running",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "build"}}}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/workers?session="+sess.ID, nil)
	req.Host, req.RemoteAddr = "127.0.0.1", "127.0.0.1:5000"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var got struct {
		Workers []webWorker `json:"workers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	found := false
	for _, w := range got.Workers {
		if w.ID == "worker-41" {
			found = true
			// Session lets the GUI settle a card restored from history (1.0.36).
			if w.Status != agent.WorkerStatusInterrupted || !w.Resumable || w.LastError == "" || w.Session != sess.ID {
				t.Fatalf("orphan row = %+v", w)
			}
		}
	}
	if !found {
		t.Fatalf("saved worker missing: %s", rec.Body.String())
	}
	// Next run's coordinator receives the interruption notice.
	if err := store.SaveWorker(ctx, session.WorkerRow{SessionID: sess.ID, ID: "worker-42", Agent: "code", Description: "b", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := session.NewWriter(store, sess.ID).AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "earlier"}); err != nil {
		t.Fatal(err)
	}
	seen := make(chan bool, 1)
	srv.eng.mu.Lock()
	srv.eng.prov = providerFunc(func(msgs []llm.Message) {
		hit := false
		for _, m := range msgs {
			hit = hit || (m.Name == llm.WorkerNotificationName && strings.Contains(m.Content, "worker-42") && strings.Contains(m.Content, "interrupted"))
		}
		select {
		case seen <- hit:
		default:
		}
	})
	srv.eng.mu.Unlock()
	if err := srv.eng.runStream(ctx, "status?", sess.ID, "", func(wireEvent) {}); err != nil {
		t.Fatal(err)
	}
	if !<-seen {
		t.Fatal("coordinator was not told about the interrupted worker")
	}
}

type providerFunc func([]llm.Message)

func (providerFunc) Name() string { return "observe" }
func (p providerFunc) Complete(_ context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p(msgs)
	return workerSteeringWireDeltas(llm.Delta{Content: "ok", FinishReason: "stop"}), nil
}
