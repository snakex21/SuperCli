package webgui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/storage/goal"
	systemhostpower "supercli/internal/system/hostpower"
)

func TestHostPowerFollowupQueueWhileRunWorking(t *testing.T) {
	for _, access := range []string{"local", "remote_token", "paired_phone"} {
		t.Run(access, func(t *testing.T) {
			s, controller, clock := setupWebHostPowerRun(t)
			var phoneCookie *http.Cookie
			if access == "remote_token" {
				s.allowRemote = true
				s.sessionToken = newSessionToken()
			}
			if access == "paired_phone" {
				setupTestPhone(t, s)
				phoneCookie = pairTestPhone(t, s)
			}
			handler := s.Handler()
			request := func(method, path, body string) *httptest.ResponseRecorder {
				host, origin, peer := "127.0.0.1", "http://127.0.0.1", "127.0.0.1:1234"
				if access == "remote_token" {
					host, origin, peer = "remote.example", "https://remote.example", "203.0.113.8:3210"
				}
				if access == "paired_phone" {
					host = s.phone.manager.Status().Host
					origin = "https://" + host
				}
				r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
				r.RemoteAddr = peer
				r.Header.Set("Origin", origin)
				if access == "remote_token" {
					r.Header.Set("Authorization", "Bearer "+s.sessionToken)
				}
				if phoneCookie != nil {
					r.AddCookie(phoneCookie)
					r.Header.Set("Cf-Ray", "fixture")
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			s.eng.mu.Lock()
			provider := s.eng.prov.(*webHostPowerProvider)
			provider.workEntered, provider.workRelease = make(chan struct{}), make(chan struct{})
			s.eng.mu.Unlock()
			var release sync.Once
			releaseWork := func() { release.Do(func() { close(provider.workRelease) }) }
			ctx, _, finish := s.eng.beginChatRun(context.Background())
			result := make(chan error, 1)
			defer func() { releaseWork(); finish() }()
			go func() {
				result <- s.eng.runStreamWithImages(ctx, "Finish fixture work then power off", "", "", nil, func(event wireEvent) {
					if event.Type == "question" {
						answerWebHostPower(t, s.eng, event)
					}
				})
			}()
			select {
			case <-provider.workEntered:
			case err := <-result:
				t.Fatalf("ended before work: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("no blocked fixture work")
			}
			if controller.Snapshot().Status != "armed" {
				t.Fatalf("not armed: %+v", controller.Snapshot())
			}
			if w := request(http.MethodGet, "/api/tasks?state=1", ""); w.Code != 200 {
				t.Fatalf("queue GET=%d", w.Code)
			}
			if state := decodeWebHostPower(t, request(http.MethodGet, "/api/host-power", "")); state.Status != "armed" {
				t.Fatal("read/status revoked consent")
			}
			for _, invalid := range []string{
				`{"prompt":"partial"`, `{"prompt":"prefix"} {}`, `{"prompt":"prefix"} trailing`, `{"prompt":"  "}`, `null`,
				`{"prompt":"follow-up","attachments":["missing-file.txt"]}`,
				`{"prompt":"follow-up","attachments":["../outside.txt"]}`,
			} {
				if w := request(http.MethodPost, "/api/tasks", invalid); w.Code != http.StatusBadRequest {
					t.Fatalf("invalid body %q: %d %s", invalid, w.Code, w.Body.String())
				}
				if controller.Snapshot().Status != "armed" {
					t.Fatal("invalid body changed pending consent")
				}
				tasks, err := s.eng.queuedTasks(context.Background())
				if err != nil || len(tasks) != 0 {
					t.Fatalf("invalid submission changed queue: %+v %v", tasks, err)
				}
			}
			attachment := stageQueueAttachment(t, s, "followup.png")
			start := time.Now()
			w := request(http.MethodPost, "/api/tasks", fmt.Sprintf(`{"prompt":"Also retain a short summary for tomorrow.","attachments":[%q]}`, attachment))
			if w.Code != 200 {
				t.Fatalf("human enqueue=%d %s", w.Code, w.Body.String())
			}
			tasks, err := s.eng.queuedTasks(context.Background())
			if err != nil || len(tasks) != 1 || len(tasks[0].Attachments) != 1 || tasks[0].Attachments[0] != attachment {
				t.Fatalf("valid attachment must be queued exactly once: %+v %v", tasks, err)
			}
			if state := decodeWebHostPower(t, request(http.MethodGet, "/api/host-power", "")); state.Status != "cancelled" {
				t.Fatalf("human followup left consent live: %+v", state)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("cancelled state not visible within polling interval")
			}
			if ctx.Err() != nil {
				t.Fatal("queue submission cancelled original run")
			}
			select {
			case err := <-result:
				t.Fatalf("main work interrupted: %v", err)
			default:
			}
			g, err := provider.svc.Goal(context.Background(), provider.goalID)
			if err != nil || g.Status != goal.StatusActive {
				t.Fatalf("withdrawal finished the goal: %+v %v", g, err)
			}
			releaseWork()
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("original work failed to finish")
			}
			g, err = provider.svc.Goal(context.Background(), provider.goalID)
			if err != nil || g.Status != goal.StatusDone {
				t.Fatalf("original work was not completed: %+v %v", g, err)
			}
			clock.fire()
			clock.fire()
			if controller.Snapshot().Status != "cancelled" || clock.executed.Load() != 0 {
				t.Fatal("run completion restored revoked power consent")
			}
		})
	}
}

func TestHostPowerFollowupQueueEditAndGuards(t *testing.T) {
	s := newTestServer(t, true)
	controller, clock := installWebHostPower(t, s.eng)
	task, err := s.eng.enqueueTask(context.Background(), "", "original queued prompt")
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	request := func(method, body, origin string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://remote.example/api/tasks", strings.NewReader(body))
		r.RemoteAddr = "203.0.113.8:3210"
		if auth {
			r.Header.Set("Authorization", "Bearer "+s.sessionToken)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	armWebHostPower(t, controller)
	for _, tc := range []struct {
		method, body, origin string
		auth                 bool
		code                 int
	}{
		{http.MethodPost, `{"prompt":"untrusted"}`, "", false, 401},
		{http.MethodPost, `{"prompt":"cross-origin"}`, "https://evil.example", true, 403},
		{http.MethodPatch, fmt.Sprintf(`{"id":%q,"prompt":"cross-origin"}`, task.ID), "https://evil.example", true, 403},
		{http.MethodPatch, fmt.Sprintf(`{"id":%q,"prompt":"partial"`, task.ID), "", true, 400},
		{http.MethodPatch, `{"id":"missing","prompt":"not a queued task"}`, "", true, 404},
		{http.MethodPatch, fmt.Sprintf(`{"id":%q,"prompt":" "}`, task.ID), "", true, 400},
		{http.MethodPatch, fmt.Sprintf(`{"id":%q,"prompt":"original queued prompt"}`, task.ID), "", true, 200},
		{http.MethodPatch, fmt.Sprintf(`{"id":%q,"position":0}`, task.ID), "", true, 200},
		{http.MethodGet, "", "", true, 200},
	} {
		if w := request(tc.method, tc.body, tc.origin, tc.auth); w.Code != tc.code {
			t.Fatalf("%s %s: got %d want %d (%s)", tc.method, tc.body, w.Code, tc.code, w.Body.String())
		}
		if controller.Snapshot().Status != "armed" {
			t.Fatal("invalid, untrusted, unchanged or read request revoked consent")
		}
	}
	w := request(http.MethodPatch, fmt.Sprintf(`{"id":%q,"prompt":"任意的新指示"}`, task.ID), "", true)
	if w.Code != 200 || controller.Snapshot().Status != "cancelled" {
		t.Fatalf("changed human prompt did not revoke consent: %d %+v", w.Code, controller.Snapshot())
	}
	controller.Finish("fixture-job", true)
	clock.fire()
	if clock.executed.Load() != 0 {
		t.Fatal("revoked consent executed")
	}
	// A subsequent root boundary cannot bring memory-only cancelled consent back.
	controller.Begin("next-root")
	controller.Finish("next-root", true)
	clock.fire()
	if controller.Snapshot().Status != "cancelled" || clock.executed.Load() != 0 {
		t.Fatal("root boundary restored cancelled authorization")
	}
}

func TestHostPowerFollowupRevokesBeforeQueueWriteFence(t *testing.T) {
	s := newTestServer(t, false)
	controller, clock := installWebHostPower(t, s.eng)
	armWebHostPower(t, controller)
	attachment := stageQueueAttachment(t, s, "before-write.png")
	s.queueClaimsMu.Lock()
	var unlock sync.Once
	defer unlock.Do(s.queueClaimsMu.Unlock)
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response <- queueAttachmentRequest(t, s, http.MethodPost, "/api/tasks", fmt.Sprintf(`{"prompt":"new trusted scope before storage can proceed","attachments":[%q]}`, attachment))
	}()
	deadline := time.Now().Add(time.Second)
	for controller.Snapshot().Status != "cancelled" {
		if time.Now().After(deadline) {
			t.Fatal("queued human instruction waited for I/O before revoking consent")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-response:
		t.Fatal("queue write bypassed its claim fence")
	default:
	}
	unlock.Do(s.queueClaimsMu.Unlock)
	select {
	case w := <-response:
		if w.Code != http.StatusOK {
			t.Fatalf("queue write=%d %s", w.Code, w.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queue did not proceed after storage fence released")
	}
	controller.Finish("fixture-job", true)
	clock.fire()
	if clock.executed.Load() != 0 {
		t.Fatal("consent survived a delayed queue write")
	}
}

func TestHostPowerFollowupHonestAfterExecutionClaim(t *testing.T) {
	s := newTestServer(t, false)
	entered, release := make(chan struct{}), make(chan struct{})
	var callback func()
	now := time.Now()
	controller := systemhostpower.New(systemhostpower.Options{Execute: func(context.Context) error { close(entered); <-release; return nil }, Now: func() time.Time { return now }, AfterFunc: func(_ time.Duration, f func()) func() { callback = f; return func() {} }})
	s.eng.hostPowerMu.Lock()
	s.eng.hostPower = controller
	s.eng.hostPowerMu.Unlock()
	t.Cleanup(controller.Close)
	armWebHostPower(t, controller)
	controller.Finish("fixture-job", true)
	now = now.Add(time.Minute)
	finished := make(chan struct{})
	go func() { callback(); close(finished) }()
	<-entered
	w := queueAttachmentRequest(t, s, http.MethodPost, "/api/tasks", `{"prompt":"new human instruction after claim"}`)
	if w.Code != 200 || controller.Snapshot().Status != "executing" {
		t.Errorf("claimed execution was falsely called cancelled: %d %+v", w.Code, controller.Snapshot())
	}
	close(release)
	<-finished
	if state := controller.Snapshot(); state.Status != "executed" {
		t.Fatalf("lost real claimed result: %+v", state)
	}
}
