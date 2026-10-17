package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"supercli/internal/storage/session"
)

func queuedChatRequest(s *Server, id string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.handleChat(rec, httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(fmt.Sprintf(`{"queue_task_id":%q,"prompt":"stale client copy"}`, id))))
	return rec
}

func TestQueuedChatConcurrentClientsExactlyOnce(t *testing.T) {
	s := newTestServer(t, false)
	task, err := s.eng.enqueueTask(context.Background(), "", "authoritative queued prompt")
	if err != nil {
		t.Fatal(err)
	}
	const clients = 12
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results <- queuedChatRequest(s, task.ID) }()
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, duplicate := 0, 0
	for rec := range results {
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if strings.Contains(body, `"type":"queue_claimed"`) {
			duplicate++
			if strings.Contains(body, `"type":"error"`) {
				t.Fatal(body)
			}
			continue
		}
		if !strings.Contains(body, `"type":"session_activity"`) || !strings.Contains(body, "authoritative queued prompt") || strings.Contains(body, "stale client copy") {
			t.Fatal(body)
		}
		accepted++
	}
	if accepted != 1 || duplicate != clients-1 {
		t.Fatalf("accepted=%d duplicates=%d", accepted, duplicate)
	}
	if tasks := listQueuedOverHTTP(t, s); len(tasks) != 0 {
		t.Fatalf("queue: %+v", tasks)
	}
	if rec := queuedChatRequest(s, task.ID); !strings.Contains(rec.Body.String(), `"type":"queue_claimed"`) {
		t.Fatal("late replay not deduplicated", rec.Body.String())
	}
}

func TestQueuedChatFailureBeforeAcceptanceReleasesClaim(t *testing.T) {
	s := newTestServer(t, false)
	// An attachment can disappear after enqueue; validation must not consume work.
	path := filepath.Join(s.eng.Home(), "queued.txt")
	if err := os.WriteFile(path, []byte("queued file"), 0600); err != nil {
		t.Fatal(err)
	}
	task, err := s.eng.enqueueTaskWithAttachments(context.Background(), "", "inspect", []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if rec := queuedChatRequest(s, task.ID); rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d %s", rec.Code, rec.Body.String())
	}
	if tasks := listQueuedOverHTTP(t, s); len(tasks) != 1 {
		t.Fatal(tasks)
	}
	if err := os.WriteFile(path, []byte("restored"), 0600); err != nil {
		t.Fatal(err)
	}
	rec := queuedChatRequest(s, task.ID)
	if !strings.Contains(rec.Body.String(), `"type":"session_activity"`) {
		t.Fatal(rec.Body.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("accepted task removed attachment", err)
	}
	if tasks := listQueuedOverHTTP(t, s); len(tasks) != 0 {
		t.Fatal(tasks)
	}
}

func TestQueuedChatSetupFailureRetainsTask(t *testing.T) {
	s := newTestServer(t, false)
	s.eng.SetAppProfile("nestcafe")
	task, err := s.eng.enqueueTask(context.Background(), "", "retain me")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		rec := queuedChatRequest(s, task.ID)
		if !strings.Contains(rec.Body.String(), `"err_code":"chat.noProvider"`) {
			t.Fatal(rec.Body.String())
		}
	}
	if tasks := listQueuedOverHTTP(t, s); len(tasks) != 1 {
		t.Fatal(tasks)
	}
}

func TestQueueMutationsBroadcastToBothDevices(t *testing.T) {
	s := newTestServer(t, false)
	pc, _, closePC := s.eng.syncHub.subscribe("", "pc")
	defer closePC()
	phone, _, closePhone := s.eng.syncHub.subscribe("", "phone")
	defer closePhone()
	check := func() {
		t.Helper()
		for _, sub := range []*syncSubscriber{pc, phone} {
			select {
			case f := <-sub.events:
				if f.Type != "queue_changed" || f.ProjectPath != s.eng.Home() {
					t.Fatalf("frame=%+v", f)
				}
			default:
				t.Fatal("missing queue_changed")
			}
		}
	}
	rec := queueAttachmentRequest(t, s, http.MethodPost, "/api/tasks", `{"prompt":"queued"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	check()
	var task session.QueuedTask
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	rec = queueAttachmentRequest(t, s, http.MethodPatch, "/api/tasks", fmt.Sprintf(`{"id":%q,"prompt":"edited"}`, task.ID))
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	check()
	claim, err := s.claimQueuedChat(context.Background(), &chatRequest{QueueTaskID: task.ID})
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	if rec := queueAttachmentRequest(t, s, http.MethodDelete, "/api/tasks?id="+task.ID, ""); rec.Code != 409 {
		t.Fatal("destructive delete during claim", rec.Code)
	}
	claim.accept()
	check()
	claim.finish()
	if rec := queueAttachmentRequest(t, s, http.MethodDelete, "/api/tasks?keep_attachments=1&id="+task.ID, ""); rec.Code != 200 {
		t.Fatal("legacy accepted cleanup not idempotent", rec.Code)
	}
	check()
	rec = queueAttachmentRequest(t, s, http.MethodPost, "/api/tasks", `{"prompt":"delete me"}`)
	check()
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if rec := queueAttachmentRequest(t, s, http.MethodDelete, "/api/tasks?id="+task.ID, ""); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	check()
}

func TestQueueStateDistinguishesIdleAndRunningWorkspace(t *testing.T) {
	s := newTestServer(t, false)
	check := func(want bool) {
		t.Helper()
		rec := queueAttachmentRequest(t, s, http.MethodGet, "/api/tasks?state=1", "")
		var state struct {
			Running bool                 `json:"running"`
			Tasks   []session.QueuedTask `json:"tasks"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		if state.Running != want {
			t.Fatalf("running=%v want %v", state.Running, want)
		}
	}
	check(false)
	run := s.eng.syncHub.start("s", "prompt", syncIdentity{}, func() {}, syncFrame{ProjectPath: s.eng.Home()})
	check(true)
	s.eng.syncHub.finish(run, nil)
	check(false)
}

func TestSyncStopAndErrorPauseQueueForObservers(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprint(stop), func(t *testing.T) {
			var h syncHub
			sub, _, closeSub := h.subscribe("s", "observer")
			defer closeSub()
			run := h.start("s", "prompt", syncIdentity{}, func() {})
			if stop {
				if !h.stop("s", run.info.TurnID) {
					t.Fatal("stop failed")
				}
			} else {
				h.event(run, wireEvent{Type: "error", Err: "failed"})
			}
			h.finish(run, nil)
			for len(sub.events) > 0 {
				f := <-sub.events
				if f.Type == "run_finished" {
					if !f.QueuePaused {
						t.Fatal("queue would restart after Stop/error")
					}
					return
				}
			}
			t.Fatal("missing finish")
		})
	}
}
