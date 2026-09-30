package webgui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/goal"
	"supercli/internal/storage/session"
)

func TestIsLoopbackHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"localhost:8080", true},
		{"127.0.0.1", true},
		{"127.0.0.1:5000", true},
		{"::1", true},
		{"[::1]:9000", true},
		{"", true},
		{"example.com", false},
		{"example.com:80", false},
		{"10.0.0.5", false},
		{"192.168.1.10:3000", false},
	}
	for _, c := range cases {
		if got := isLoopbackHost(c.host); got != c.want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestQueryInt(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?a=5&b=abc&c=-3&d=0", nil)
	if got := queryInt(r, "a", 10); got != 5 {
		t.Errorf("a = %d, want 5", got)
	}
	if got := queryInt(r, "b", 10); got != 10 {
		t.Errorf("b malformed = %d, want default 10", got)
	}
	if got := queryInt(r, "c", 10); got != 10 {
		t.Errorf("c negative = %d, want default 10", got)
	}
	if got := queryInt(r, "d", 10); got != 10 {
		t.Errorf("d zero = %d, want default 10", got)
	}
	if got := queryInt(r, "missing", 7); got != 7 {
		t.Errorf("missing = %d, want default 7", got)
	}
}

// newTestServer builds a Server backed by an echo engine in a temp
// data dir — no network, no API key, deterministic.
func newTestServer(t *testing.T, allowRemote bool) *Server {
	t.Helper()
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	return NewServer(eng, allowRemote)
}

func TestLocalGuard_BlocksRemoteHost(t *testing.T) {
	srv := newTestServer(t, false)
	h := srv.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Host = "evil.example.com"
	req.RemoteAddr = "127.0.0.1:43210"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("remote host: status = %d, want 403", rec.Code)
	}
}

func TestLocalGuard_BlocksRemotePeerWithSpoofedLoopbackHost(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Host = "127.0.0.1:1234"
	req.RemoteAddr = "203.0.113.8:43210"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("spoofed loopback Host status = %d, want 403", rec.Code)
	}
}

func TestLocalGuard_AllowsLoopback(t *testing.T) {
	srv := newTestServer(t, false)
	h := srv.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Host = "127.0.0.1:1234"
	req.RemoteAddr = "127.0.0.1:43210"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("loopback: status = %d, want 200", rec.Code)
	}
}

func TestAllowRemoteRequiresSessionTokenForAPI(t *testing.T) {
	srv := newTestServer(t, true)
	h := srv.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Host = "evil.example.com"
	req.RemoteAddr = "203.0.113.8:43210"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("allowRemote without token: status = %d, want 401", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Host = "evil.example.com"
	req.RemoteAddr = "203.0.113.8:43210"
	req.Header.Set("Authorization", "Bearer "+srv.sessionToken)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("allowRemote with token: status = %d, want 200", rec.Code)
	}
}

func TestAllowRemoteBrowserUsesNativeBasicSignIn(t *testing.T) {
	srv := newTestServer(t, true)
	request := func(password string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "workstation.example:8080"
		req.RemoteAddr = "203.0.113.8:43210"
		if password != "" {
			req.SetBasicAuth("supercli", password)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}
	unauthorized := request("")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("remote UI without password status=%d", unauthorized.Code)
	}
	challenges := unauthorized.Header().Values("WWW-Authenticate")
	if len(challenges) < 1 || !strings.HasPrefix(challenges[0], "Basic ") {
		t.Fatalf("authentication challenges = %v", challenges)
	}
	if wrong := request("wrong"); wrong.Code != http.StatusUnauthorized {
		t.Fatalf("remote UI with wrong password status=%d", wrong.Code)
	}
	if authenticated := request(srv.sessionToken); authenticated.Code != http.StatusOK {
		t.Fatalf("remote UI with token status=%d body=%s", authenticated.Code, authenticated.Body.String())
	}
}

func TestAllowRemoteLocalBootstrapSetsHttpOnlyCookie(t *testing.T) {
	srv := newTestServer(t, true)
	req := httptest.NewRequest(http.MethodGet, localSessionBootstrap, nil)
	req.Host = "127.0.0.1:1234"
	req.RemoteAddr = "127.0.0.1:43210"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("bootstrap status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != remoteSessionCookie || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("bootstrap cookies = %+v", cookies)
	}
	apiReq := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	apiReq.Host = "127.0.0.1:1234"
	apiReq.RemoteAddr = "127.0.0.1:43210"
	apiReq.AddCookie(cookies[0])
	apiRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Fatalf("cookie-authenticated API status=%d body=%s", apiRec.Code, apiRec.Body.String())
	}
}

func TestAllowRemoteBootstrapRejectsRemotePeer(t *testing.T) {
	srv := newTestServer(t, true)
	req := httptest.NewRequest(http.MethodGet, localSessionBootstrap, nil)
	req.Host = "127.0.0.1:1234"
	req.RemoteAddr = "203.0.113.8:43210"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("remote bootstrap status=%d cookies=%v", rec.Code, rec.Result().Cookies())
	}
}

func TestHandleHealth(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	srv.handleHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
	if body["provider_type"] != "echo" || body["chat_ready"] != true {
		t.Errorf("provider health = type %v ready %v", body["provider_type"], body["chat_ready"])
	}
}

func TestHandleHealthReportsBlockedNestCafeEcho(t *testing.T) {
	srv := newTestServer(t, false)
	srv.eng.SetAppProfile("nestcafe")
	rec := httptest.NewRecorder()
	srv.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true || body["provider_type"] != "echo" || body["chat_ready"] != false {
		t.Fatalf("blocked provider health = %+v", body)
	}
}

func TestEmbeddedUIIncludesGoalInspector(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "127.0.0.1"
	req.RemoteAddr = "127.0.0.1:43210"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("index status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, marker := range []string{`data-tab="goal"`, `id="tab-goal"`, `id="goal-side-help"`, `id="manage-side-goal"`} {
		if !strings.Contains(body, marker) {
			t.Errorf("embedded UI missing %s", marker)
		}
	}
}

func TestHandleGoal_CreateManageAndInject(t *testing.T) {
	srv := newTestServer(t, false)
	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/goal", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		srv.handleGoal(rec, req)
		return rec
	}

	created := post(`{"action":"set","title":"Ship web goals","description":"shared state","success_criteria":"all tests green"}`)
	if created.Code != http.StatusOK {
		t.Fatalf("set status = %d: %s", created.Code, created.Body.String())
	}
	var view goalView
	if err := json.Unmarshal(created.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Title != "Ship web goals" || view.Description != "shared state" || view.SuccessCriteria != "all tests green" {
		t.Fatalf("created goal = %+v", view)
	}

	added := post(`{"action":"add_task","title":"Wire the panel"}`)
	if added.Code != http.StatusOK {
		t.Fatalf("add task status = %d: %s", added.Code, added.Body.String())
	}
	if err := json.Unmarshal(added.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Tasks) != 1 || view.Tasks[0].Title != "Wire the panel" || view.Tasks[0].Status != "pending" {
		t.Fatalf("tasks after add = %+v", view.Tasks)
	}

	completed := post(`{"action":"set_task_status","task_seq":1,"status":"done"}`)
	if completed.Code != http.StatusOK {
		t.Fatalf("complete status = %d: %s", completed.Code, completed.Body.String())
	}
	if err := json.Unmarshal(completed.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Tasks[0].Status != "done" {
		t.Fatalf("task status = %+v", view.Tasks[0])
	}
	if !view.ReadyForVerification || view.CanFinish {
		t.Fatalf("readiness after completing tasks = %+v", view)
	}

	blocked := post(`{"action":"set_status","status":"done"}`)
	if blocked.Code != http.StatusBadRequest || !strings.Contains(blocked.Body.String(), "verification required") {
		t.Fatalf("unverified finish status = %d: %s", blocked.Code, blocked.Body.String())
	}
	verified := post(`{"action":"verify","passed":true,"text":"go test ./internal/webgui passed"}`)
	if verified.Code != http.StatusOK {
		t.Fatalf("verify status = %d: %s", verified.Code, verified.Body.String())
	}
	if err := json.Unmarshal(verified.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.VerificationStatus != "passed" || !view.CanFinish || view.VerificationEvidence == "" {
		t.Fatalf("verified view = %+v", view)
	}

	capture := &prefixCaptureProvider{}
	srv.eng.prov = capture
	loop, err := srv.eng.newLoop()
	if err != nil {
		t.Fatal(err)
	}
	messages := loop.AllMessages()
	if len(messages) == 0 || strings.Contains(messages[0].Content, "[current_goal]") {
		t.Fatal("goal rewrote stable prompt")
	}
	events, err := loop.Run(context.Background(), "Report the goal progress.")
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	if len(capture.requests) != 1 {
		t.Fatalf("model calls=%d", len(capture.requests))
	}
	var request strings.Builder
	for _, message := range capture.requests[0] {
		request.WriteString(message.Content)
	}
	for _, want := range []string{"[current_goal]", "Ship web goals", "verification: passed"} {
		if !strings.Contains(request.String(), want) {
			t.Fatalf("missing current goal %q: %s", want, request.String())
		}
	}

	finished := post(`{"action":"set_status","status":"done"}`)
	if finished.Code != http.StatusOK || strings.TrimSpace(finished.Body.String()) != "null" {
		t.Fatalf("verified finish status = %d: %s", finished.Code, finished.Body.String())
	}
}

func TestHandleGoal_RejectsInvalidMutation(t *testing.T) {
	srv := newTestServer(t, false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/goal", strings.NewReader(`{"action":"set","title":""}`))
	srv.handleGoal(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty title status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleTranscript_MissingID(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodGet, "/api/transcript", nil)
	rec := httptest.NewRecorder()
	srv.handleTranscript(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing id: status = %d, want 400", rec.Code)
	}
}

func TestHandleTranscript_PagedAndLegacyContracts(t *testing.T) {
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "echo-test", "paged")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	for _, content := range []string{"one", "two", "three"} {
		if err := writer.AppendMessage(context.Background(), llm.Message{Role: llm.RoleUser, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	attachmentPath := filepath.Join(srv.eng.Home(), "photo.png")
	if err := store.SaveMessageAttachments(context.Background(), sess.ID, 2, []string{attachmentPath}); err != nil {
		t.Fatal(err)
	}

	paged := httptest.NewRecorder()
	srv.handleTranscript(paged, httptest.NewRequest(http.MethodGet, "/api/transcript?id="+sess.ID+"&limit=2", nil))
	if paged.Code != http.StatusOK {
		t.Fatalf("paged status = %d: %s", paged.Code, paged.Body.String())
	}
	var page transcriptPage
	if err := json.Unmarshal(paged.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || page.BeforeSeq != 2 || len(page.Messages) != 2 || page.Messages[0].Content != "two" || len(page.Messages[0].Attachments) != 1 || page.Messages[0].Attachments[0] != attachmentPath {
		t.Fatalf("paged transcript = %+v", page)
	}

	legacy := httptest.NewRecorder()
	srv.handleTranscript(legacy, httptest.NewRequest(http.MethodGet, "/api/transcript?id="+sess.ID, nil))
	var messages []transcriptMsg
	if err := json.Unmarshal(legacy.Body.Bytes(), &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || messages[0].Content != "one" || len(messages[1].Attachments) != 1 || messages[1].Attachments[0] != attachmentPath {
		t.Fatalf("legacy transcript = %+v", messages)
	}
}

func TestRecordMessageAttachmentsRequiresNewUserMessage(t *testing.T) {
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "echo-test", "attachments")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	ctx := context.Background()
	if err := writer.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "old"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(srv.eng.Home(), "new-photo.png")
	if err := srv.recordMessageAttachments(ctx, sess.ID, 1, []string{path}); err == nil {
		t.Fatal("recordMessageAttachments attached a new image to the previous user message")
	}
	if err := writer.AppendMessage(ctx, llm.Message{Role: llm.RoleUser, Content: "new"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.recordMessageAttachments(ctx, sess.ID, 1, []string{path}); err != nil {
		t.Fatal(err)
	}
	attachments, err := store.ReadMessageAttachmentsRange(ctx, sess.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || len(attachments[2]) != 1 || attachments[2][0] != path {
		t.Fatalf("attachments = %#v", attachments)
	}
}

func TestHandleGoalGlobalScopeAndLegacyAssignment(t *testing.T) {
	srv := newTestServer(t, false)
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.handleGoal(rec, httptest.NewRequest(http.MethodPost, "/api/goal", strings.NewReader(body)))
		return rec
	}
	project := post(`{"action":"set","title":"Project goal"}`)
	global := post(`{"action":"set","scope":"global","title":"General goal"}`)
	var p, g goalView
	if project.Code != http.StatusOK || global.Code != http.StatusOK {
		t.Fatalf("create: %s %s", project.Body.String(), global.Body.String())
	}
	if err := json.Unmarshal(project.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(global.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if p.Scope != "project" || g.Scope != "global" || p.ID == g.ID {
		t.Fatalf("wrong scopes: %+v %+v", p, g)
	}
	current, err := srv.eng.activeGoal(context.Background())
	if err != nil || current.ID != p.ID {
		t.Fatal("global creation replaced project goal")
	}
	if bad := post(`{"action":"set","scope":"other-project","title":"wrong"}`); bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid scope accepted: %s", bad.Body.String())
	}
	service, err := srv.eng.goalService(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	legacyStore := goal.NewStorage(srv.eng.goalDB)
	legacy, err := goal.NewService(legacyStore).Set(context.Background(), "Old Windows goal", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	unassigned := httptest.NewRecorder()
	srv.handleGoal(unassigned, httptest.NewRequest(http.MethodGet, "/api/goal?unassigned=1", nil))
	if unassigned.Code != http.StatusOK || !strings.Contains(unassigned.Body.String(), legacy.ID) {
		t.Fatal("legacy goal disappeared")
	}
	body, _ := json.Marshal(goalMutation{Action: "assign", GoalID: legacy.ID, Target: "project"})
	assigned := post(string(body))
	if assigned.Code != http.StatusOK {
		t.Fatalf("assignment: %s", assigned.Body.String())
	}
	if active, _ := service.Refresh(context.Background()); active.ID != legacy.ID {
		t.Fatal("legacy goal not assigned")
	}
}

func TestHandleGoalRejectsActionsFromAnotherProjectPanel(t *testing.T) {
	for _, action := range []goalMutation{
		{Action: "add_task", Title: "Old panel task"},
		{Action: "add_note", Text: "Old panel note"},
		{Action: "set_task_status", TaskSeq: 1, Status: "done"},
		{Action: "set_status", Status: "abandoned"},
	} {
		t.Run(action.Action, func(t *testing.T) {
			ctx := context.Background()
			srv := newTestServer(t, false)
			a, err := srv.eng.goalService(ctx)
			if err != nil {
				t.Fatal(err)
			}
			old, err := a.Set(ctx, "Project A", "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			srv.eng.setHome(t.TempDir())
			b, err := srv.eng.goalService(ctx)
			if err != nil {
				t.Fatal(err)
			}
			current, err := b.Set(ctx, "Project B", "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.AddTask(ctx, current.ID, "Keep pending"); err != nil {
				t.Fatal(err)
			}
			action.GoalID = old.ID
			body, err := json.Marshal(action)
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			srv.handleGoal(rec, httptest.NewRequest(http.MethodPost, "/api/goal", strings.NewReader(string(body))))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("stale project action accepted: %d %s", rec.Code, rec.Body.String())
			}
			got, err := b.Goal(ctx, current.ID)
			if err != nil {
				t.Fatal(err)
			}
			tasks, err := b.ListTasks(ctx, current.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != goal.StatusActive || got.Notes != "" || len(tasks) != 1 || tasks[0].Status != goal.TaskPending {
				t.Fatalf("wrong project mutated: %+v %+v", got, tasks)
			}
		})
	}
}
