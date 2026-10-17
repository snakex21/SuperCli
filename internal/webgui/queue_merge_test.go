package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

func TestQueueMergeSplit(t *testing.T) {
	tasks := []session.QueuedTask{{Prompt: "first", Attachments: []string{"a.jpg", "b.png"}}, {Prompt: "second", Attachments: []string{"c.txt"}}}
	size := func(string) (int64, error) { return 1, nil }
	batches, err := splitQueuedMessages(tasks, "Wiadomość {index}/{count}", queueAttachmentLimits["anthropic"], size)
	if err != nil || len(batches) != 1 || batches[0].Prompt != "Wiadomość 1/2\nfirst\n\nWiadomość 2/2\nsecond" || !reflect.DeepEqual(batches[0].Attachments, []string{"a.jpg", "b.png", "c.txt"}) {
		t.Fatalf("merge: %+v, %v", batches, err)
	}
	for _, tc := range []struct {
		name   string
		images int
		bytes  int64
		files  int
		want   int
	}{
		{"image boundary", 2, 100, 3, 2}, {"three batches", 1, 100, 3, 3}, {"bytes", 50, 2, 3, 2}, {"transport count", 100, 100, 9, 2}, {"four batches refused", 1, 100, 4, 0}, {"oversize file", 5, 0, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := []string{}
			for i := 0; i < tc.files; i++ {
				paths = append(paths, fmt.Sprintf("%d.jpg", i))
			}
			got, err := splitQueuedMessages([]session.QueuedTask{{Prompt: "one", Attachments: paths}}, "Message {index}/{count}", queueAttachmentLimit{tc.images, tc.bytes}, size)
			if tc.want == 0 {
				if err == nil {
					t.Fatal("unsafe merge accepted")
				}
				return
			}
			if err != nil || len(got) != tc.want {
				t.Fatalf("split: %+v, %v", got, err)
			}
			all := []string{}
			for _, batch := range got {
				all = append(all, batch.Attachments...)
				if !strings.HasPrefix(batch.Prompt, "Message 1/1\none") {
					t.Fatal(batch.Prompt)
				}
			}
			if !reflect.DeepEqual(all, paths) {
				t.Fatal("attachments lost or reordered")
			}
		})
	}
	for name, want := range map[string]queueAttachmentLimit{"anthropic": {20, 30 << 20}, "openai": {50, 45 << 20}, "codex": {50, 45 << 20}, "gemini": {100, 45 << 20}, "ollama": {5, 20 << 20}, "lmstudio": {5, 20 << 20}, "other": {5, 20 << 20}} {
		if got := queueLimit(name, "https://example.com"); got != want {
			t.Fatalf("%s: %+v", name, got)
		}
	}
	if queueLimit("openai", "http://localhost:1234/v1") != queueAttachmentLimits["local"] {
		t.Fatal("local openai-compatible limit")
	}
}

func TestQueueMergeDurableSyncAndClaims(t *testing.T) {
	srv := newTestServer(t, false)
	first, err := srv.eng.enqueueTask(context.Background(), "", "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := srv.eng.enqueueTask(context.Background(), "", "second")
	if err != nil {
		t.Fatal(err)
	}
	pc, _, closePC := srv.eng.syncHub.subscribe("", "pc")
	defer closePC()
	phone, _, closePhone := srv.eng.syncHub.subscribe("", "phone")
	defer closePhone()
	body := fmt.Sprintf(`{"ids":[%q,%q],"label":"Message {index}/{count}"}`, first.ID, second.ID)
	rec := queueAttachmentRequest(t, srv, http.MethodPost, "/api/tasks?merge=1", body)
	if rec.Code != 200 {
		t.Fatalf("merge: %d %s", rec.Code, rec.Body.String())
	}
	for _, sub := range []*syncSubscriber{pc, phone} {
		select {
		case ev := <-sub.events:
			if ev.Type != "queue_changed" {
				t.Fatal(ev.Type)
			}
		case <-time.After(time.Second):
			t.Fatal("missing live merge sync")
		}
	}
	rows := listQueuedOverHTTP(t, srv)
	if len(rows) != 1 || !rows[0].Merged || !strings.Contains(rows[0].Prompt, "Message 2/2\nsecond") {
		t.Fatalf("durable rows: %+v", rows)
	}
	if rec := queueAttachmentRequest(t, srv, http.MethodPost, "/api/tasks?merge=1", body); rec.Code != 409 {
		t.Fatal("stale second device merged again")
	}
	req := chatRequest{QueueTaskID: rows[0].ID}
	claim, err := srv.claimQueuedChat(context.Background(), &req)
	if err != nil || claim == nil {
		t.Fatalf("claim: %v", err)
	}
	if req.Prompt != rows[0].Prompt {
		t.Fatal("durable prompt not used")
	}
	if dup, err := srv.claimQueuedChat(context.Background(), &req); dup != nil || err != nil {
		t.Fatal("duplicate claim")
	}
	claim.accept()
	claim.finish()
	if len(listQueuedOverHTTP(t, srv)) != 0 {
		t.Fatal("merged message not retired")
	}
}

func TestQueueEditLiveSyncAndClaimFence(t *testing.T) {
	srv := newTestServer(t, false)
	item, err := srv.eng.enqueueTask(context.Background(), "", "before")
	if err != nil {
		t.Fatal(err)
	}
	a, _, closeA := srv.eng.syncHub.subscribe("", "a")
	defer closeA()
	b, _, closeB := srv.eng.syncHub.subscribe("", "b")
	defer closeB()
	image := stageQueueAttachment(t, srv, "edited.png")
	bodyBytes, _ := json.Marshal(map[string]any{"id": item.ID, "prompt": "edited", "attachments": []string{image}})
	body := string(bodyBytes)
	rec := queueAttachmentRequest(t, srv, http.MethodPatch, "/api/tasks", body)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	for _, sub := range []*syncSubscriber{a, b} {
		select {
		case ev := <-sub.events:
			if ev.Type != "queue_changed" {
				t.Fatal(ev.Type)
			}
		case <-time.After(time.Second):
			t.Fatal("missing edit sync")
		}
	}
	req := chatRequest{QueueTaskID: item.ID}
	claim, err := srv.claimQueuedChat(context.Background(), &req)
	if err != nil || claim == nil || req.Prompt != "edited" || len(req.Attachments) != 1 || req.Attachments[0] != image {
		t.Fatalf("edited claim: %+v %v", req, err)
	}
	defer claim.finish()
	if rec := queueAttachmentRequest(t, srv, http.MethodPatch, "/api/tasks", body); rec.Code != 409 {
		t.Fatal("editing dispatched row accepted")
	}
}

func queueMergeKnobReq(t *testing.T, srv *Server, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleConfigKnobs(rec, httptest.NewRequest(method, "/api/config", strings.NewReader(body)))
	return rec
}

func TestQueueMergeKnob(t *testing.T) {
	srv := newTestServer(t, false)
	if srv.queueMergeMode() != "off" {
		t.Fatal("default not off")
	}
	path, _ := config.FindTomlPaths(srv.eng.DataDir(), srv.eng.Home())
	for _, value := range []string{"auto", "off", "auto", "default"} {
		body, _ := json.Marshal(map[string]string{"key": "queue_merge", "value": value})
		if rec := queueMergeKnobReq(t, srv, http.MethodPost, string(body)); rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
		want := value
		if value == "default" {
			want = "off"
		}
		got, err := config.LoadToml(path)
		if err != nil || got.QueueMerge != want || srv.queueMergeMode() != want {
			t.Fatalf("roundtrip: %+v %v", got, err)
		}
	}
	if rec := queueMergeKnobReq(t, srv, http.MethodPost, `{"key":"queue_merge","value":"on"}`); rec.Code != 400 {
		t.Fatal("invalid merge mode accepted")
	}
}

func TestWorkerEffortWirePayload(t *testing.T) {
	wire, ok := toWireEvent(agent.WorkerProgressEvent{TaskID: "worker-1", Agent: "advisor", Kind: "started", Model: "gpt-5", ReasoningEffort: "high"})
	if !ok || wire.Model != "gpt-5" || wire.ReasoningEffort != "high" {
		t.Fatalf("wire: %+v", wire)
	}
}
