package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

type memWorkerStore struct {
	mu   sync.Mutex
	recs map[string]WorkerRecord
}

func newMemWorkerStore() *memWorkerStore { return &memWorkerStore{recs: map[string]WorkerRecord{}} }

func (m *memWorkerStore) SaveWorker(_ context.Context, rec WorkerRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec.Messages = append([]llm.Message(nil), rec.Messages...)
	m.recs[rec.SessionID+"/"+rec.ID] = rec
	return nil
}
func (m *memWorkerStore) LoadWorker(_ context.Context, sid, id string) (WorkerRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.recs[sid+"/"+id]
	return rec, ok, nil
}
func (m *memWorkerStore) ListWorkers(_ context.Context, sid string) ([]WorkerRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []WorkerRecord
	for _, rec := range m.recs {
		if rec.SessionID == sid {
			out = append(out, rec)
		}
	}
	return out, nil
}
func (m *memWorkerStore) SetWorkerStatus(_ context.Context, sid, id, status, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.recs[sid+"/"+id]
	rec.Status, rec.LastError = status, reason
	m.recs[sid+"/"+id] = rec
	return nil
}
func (m *memWorkerStore) ids() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, rec := range m.recs {
		out = append(out, rec.ID)
	}
	return out
}

type sessionWriterWithID struct {
	recordingWriter
	id string
}

func (w *sessionWriterWithID) SessionID() string { return w.id }

// One process: a coordinator with a task tool whose workers persist.
func persistentCoordinator(t *testing.T, store *memWorkerStore, child llm.Provider) (*Loop, *AgentTool) {
	t.Helper()
	reg := tools.NewRegistry()
	parent, err := NewLoop(LoopConfig{Provider: makeScriptedProvider("ok"), Registry: reg, Writer: &sessionWriterWithID{id: "sess-1"}})
	if err != nil {
		t.Fatal(err)
	}
	specs := NewSubAgentRegistry()
	MustRegisterAll(specs, BuiltinSubAgents())
	at, err := NewAgentTool(specs, parent, reg, child, nil, NewLoop)
	if err != nil {
		t.Fatal(err)
	}
	at.Workers.SetStore(store, store.ids())
	return parent, at
}

// A finished worker resumes by id after an app restart with its full saved
// conversation; the follow-up is a new turn appended to that transcript.
func TestWorkerResumesAfterRestartWithFullContext(t *testing.T) {
	store := newMemWorkerStore()
	_, first := persistentCoordinator(t, store, makeScriptedProvider("FIRST_REPORT: found the bug in parser.go"))
	result, err := first.execute(context.Background(), json.RawMessage(`{"prompt":"investigate the parser"}`))
	if err != nil || result.Err != nil {
		t.Fatalf("%v %v", err, result.Err)
	}
	rec, ok, _ := store.LoadWorker(context.Background(), "sess-1", "worker-1")
	if !ok || rec.Status != "done" || len(rec.Messages) < 2 {
		t.Fatalf("saved worker = %+v ok=%v", rec, ok)
	}

	// Restart: new process, empty registry; the store survives.
	resumed := makeScriptedProvider("SECOND_REPORT: fixed")
	_, second := persistentCoordinator(t, store, resumed)
	send := NewSendMessageToolFor(second)
	out, err := send.execute(context.Background(), json.RawMessage(`{"to":"worker-1","message":"now fix it"}`))
	if err != nil || out.Err != nil {
		t.Fatalf("resume failed: %v %v", err, out.Err)
	}
	if len(resumed.reqs) == 0 {
		t.Fatal("resumed worker made no model call")
	}
	req := resumed.reqs[0]
	if !requestContains(req, "investigate the parser") || !requestContains(req, "FIRST_REPORT") || !requestContains(req, "now fix it") {
		t.Fatal("resumed worker lost its saved context")
	}
	rec, _, _ = store.LoadWorker(context.Background(), "sess-1", "worker-1")
	if !requestContains(rec.Messages, "SECOND_REPORT") || !requestContains(rec.Messages, "FIRST_REPORT") || rec.Runs < 2 {
		t.Fatalf("follow-up not appended to the saved transcript: runs=%d", rec.Runs)
	}
	// New workers never reuse a saved id.
	if next, err := second.execute(context.Background(), json.RawMessage(`{"prompt":"another"}`)); err != nil || next.Err != nil {
		t.Fatalf("%v %v", err, next.Err)
	}
	if _, ok := second.Workers.Get("worker-2"); !ok {
		t.Fatal("new worker reused a saved id")
	}
}

// A worker that was running when the process died is reconciled to a definite
// "interrupted" state and stays resumable.
func TestOrphanWorkerReconciledToInterruptedAndResumable(t *testing.T) {
	store := newMemWorkerStore()
	_ = store.SaveWorker(context.Background(), WorkerRecord{SessionID: "sess-1", ID: "worker-7", Agent: "code", Description: "build the mod",
		Status: "running", Messages: []llm.Message{{Role: llm.RoleUser, Content: "build the mod"}, {Role: llm.RoleAssistant, Content: "PARTIAL_PROGRESS step 3"}}})
	child := makeScriptedProvider("DONE after resume")
	_, at := persistentCoordinator(t, store, child)
	changed, err := at.Workers.ReconcileSavedWorkers(context.Background(), "sess-1")
	if err != nil || len(changed) != 1 || changed[0].Status != WorkerStatusInterrupted || changed[0].LastError == "" {
		t.Fatalf("reconcile = %+v %v", changed, err)
	}
	rec, _, _ := store.LoadWorker(context.Background(), "sess-1", "worker-7")
	if rec.Status != WorkerStatusInterrupted {
		t.Fatalf("saved status = %q", rec.Status)
	}
	again, _ := at.Workers.ReconcileSavedWorkers(context.Background(), "sess-1")
	if len(again) != 0 {
		t.Fatal("reconcile is not idempotent")
	}
	out, err := NewSendMessageToolFor(at).execute(context.Background(), json.RawMessage(`{"to":"worker-7","message":"continue"}`))
	if err != nil || out.Err != nil {
		t.Fatalf("resume interrupted: %v %v", err, out.Err)
	}
	if !requestContains(child.reqs[0], "PARTIAL_PROGRESS") {
		t.Fatal("interrupted worker resumed without its saved progress")
	}
}

// A live worker of this process is never reconciled away.
func TestReconcileKeepsLiveWorker(t *testing.T) {
	store := newMemWorkerStore()
	_, at := persistentCoordinator(t, store, makeScriptedProvider("x"))
	w := at.Workers.Add("code", "live", nil)
	w.sessionID = "sess-1"
	w.setState(func(w *Worker) { w.Status = "running" })
	at.Workers.persist(w)
	changed, _ := at.Workers.ReconcileSavedWorkers(context.Background(), "sess-1")
	if len(changed) != 0 {
		t.Fatalf("live worker reconciled: %+v", changed)
	}
}

func TestUnknownWorkerWithoutSavedTranscriptStillFails(t *testing.T) {
	store := newMemWorkerStore()
	_, at := persistentCoordinator(t, store, makeScriptedProvider("x"))
	out, _ := NewSendMessageToolFor(at).execute(context.Background(), json.RawMessage(`{"to":"worker-99","message":"x"}`))
	if out.Err == nil || !strings.Contains(out.Err.Error(), "unknown worker") {
		t.Fatalf("err = %v", out.Err)
	}
}

// The audited race: a live worker finished (done or failed) in memory while
// its record still says running. Reconcile saves its real state and never
// reports it "interrupted" (no second notice for the same run).
func TestReconcileSavesAFinishedLiveWorkersRealState(t *testing.T) {
	for _, final := range []string{"done", "failed"} {
		store := newMemWorkerStore()
		_, at := persistentCoordinator(t, store, makeScriptedProvider("x"))
		w := at.Workers.Add("code", "bow", nil)
		w.sessionID = "sess-1"
		w.setState(func(w *Worker) { w.Status = "running" })
		at.Workers.persist(w) // saved as running
		w.setState(func(w *Worker) { w.Status, w.LastError = final, map[string]string{"failed": "http 429"}[final] })
		changed, err := at.Workers.ReconcileSavedWorkers(context.Background(), "sess-1")
		if err != nil || len(changed) != 0 {
			t.Fatalf("%s: finished live worker reported as reconciled: %+v %v", final, changed, err)
		}
		rec, _, _ := store.LoadWorker(context.Background(), "sess-1", w.ID)
		if rec.Status != final {
			t.Fatalf("%s: saved status = %q", final, rec.Status)
		}
	}
}
