package webgui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/goal"
	systemhostpower "supercli/internal/system/hostpower"
	"supercli/internal/tools"
)

// Manual delivery also exercises stale timer callbacks after cancellation. No
// test uses the production executor, even if it creates a production Engine.
type webHostPowerClock struct {
	mu        sync.Mutex
	callbacks []func()
	now       time.Time
	executed  atomic.Int32
}

func installWebHostPower(t *testing.T, e *Engine) (*systemhostpower.Controller, *webHostPowerClock) {
	t.Helper()
	clock := &webHostPowerClock{now: time.Now()}
	controller := systemhostpower.New(systemhostpower.Options{
		Execute: func(context.Context) error { clock.executed.Add(1); return nil },
		Now:     func() time.Time { clock.mu.Lock(); defer clock.mu.Unlock(); return clock.now },
		AfterFunc: func(_ time.Duration, callback func()) func() {
			clock.mu.Lock()
			clock.callbacks = append(clock.callbacks, callback)
			clock.mu.Unlock()
			return func() {}
		},
	})
	e.hostPowerMu.Lock()
	if e.hostPower != nil {
		e.hostPower.Close()
	}
	e.hostPower = controller
	e.hostPowerMu.Unlock()
	t.Cleanup(controller.Close)
	return controller, clock
}
func (c *webHostPowerClock) timers() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.callbacks) }
func (c *webHostPowerClock) fire() {
	c.mu.Lock()
	c.now = c.now.Add(time.Minute)
	callbacks := append([]func(){}, c.callbacks...)
	c.mu.Unlock()
	for _, f := range callbacks {
		f()
	}
}
func armWebHostPower(t *testing.T, c *systemhostpower.Controller) {
	t.Helper()
	c.Begin("fixture-job")
	if err := c.Arm("fixture-job", "fixture-goal", "Verified fixture work", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}
func decodeWebHostPower(t *testing.T, w *httptest.ResponseRecorder) systemhostpower.Snapshot {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var snapshot systemhostpower.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("status must not be cached")
	}
	return snapshot
}

func TestHostPowerAuthenticatedRemoteReadOnlyAndCancel(t *testing.T) {
	s := newTestServer(t, true)
	c, clock := installWebHostPower(t, s.eng)
	h := s.Handler()
	request := func(method, path, origin string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://remote.example"+path, strings.NewReader(`{"action":"arm","goal_id":"injected"}`))
		r.RemoteAddr = "203.0.113.8:3210"
		if auth {
			r.Header.Set("Authorization", "Bearer "+s.sessionToken)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/host-power", "/api/host-power/cancel"} {
		if w := request(http.MethodGet, path, "", false); w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s: %d", path, w.Code)
		}
	}
	for i := 0; i < 3; i++ {
		if snapshot := decodeWebHostPower(t, request(http.MethodGet, "/api/host-power", "", true)); snapshot.Status != "idle" || snapshot.JobID != "" {
			t.Fatalf("GET created work: %+v", snapshot)
		}
	}
	// If GET had begun a hidden root, this sole-root arm would fail.
	armWebHostPower(t, c)
	before := c.Snapshot()
	for _, tc := range []struct {
		method, path, origin string
		code                 int
	}{
		{http.MethodPost, "/api/host-power", "", 405},
		{http.MethodPut, "/api/host-power", "", 405},
		{http.MethodHead, "/api/host-power", "", 405},
		{http.MethodGet, "/api/host-power/cancel", "", 405},
		{http.MethodDelete, "/api/host-power/cancel", "", 405},
		{http.MethodPost, "/api/host-power/cancel", "https://evil.example", 403},
	} {
		if w := request(tc.method, tc.path, tc.origin, true); w.Code != tc.code {
			t.Fatalf("%s %s: %d, want %d", tc.method, tc.path, w.Code, tc.code)
		}
		if c.Snapshot() != before {
			t.Fatal("rejected request changed state")
		}
	}
	if snapshot := decodeWebHostPower(t, request(http.MethodGet, "/api/host-power?session=other-browser", "", true)); snapshot != before {
		t.Fatalf("state not shared: %+v", snapshot)
	}
	c.Finish("fixture-job", true)
	if clock.timers() != 1 {
		t.Fatal("missing countdown")
	}
	if snapshot := decodeWebHostPower(t, request(http.MethodPost, "/api/host-power/cancel", "http://remote.example", true)); snapshot.Status != "cancelled" || snapshot.Deadline != 0 {
		t.Fatalf("cancel: %+v", snapshot)
	}
	clock.fire()
	if clock.executed.Load() != 0 {
		t.Fatal("cancelled callback executed")
	}
}

func TestHostPowerPhoneGuardAndSafeError(t *testing.T) {
	s := newTestServer(t, false)
	c, clock := installWebHostPower(t, s.eng)
	setupTestPhone(t, s)
	cookie := pairTestPhone(t, s)
	h := s.Handler()
	const host = "test-phone.trycloudflare.com"
	if w := phoneRequest(h, "GET", host, "/api/host-power", "", nil, true); w.Code != 401 {
		t.Fatalf("unpaired: %d", w.Code)
	}
	expiry := s.phone.manager.Status().ExpiresAt
	armWebHostPower(t, c)
	if snapshot := decodeWebHostPower(t, phoneRequest(h, "GET", host, "/api/host-power?session=phone", "", cookie, true)); snapshot.Status != "armed" {
		t.Fatalf("phone: %+v", snapshot)
	}
	r := httptest.NewRequest("POST", "https://"+host+"/api/host-power/cancel", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.AddCookie(cookie)
	r.Header.Set("Cf-Ray", "test")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || c.Snapshot().Status != "armed" {
		t.Fatal("cross-site cancellation accepted")
	}
	decodeWebHostPower(t, phoneRequest(h, "POST", host, "/api/host-power/cancel", "", cookie, true))
	if !expiry.Equal(s.phone.manager.Status().ExpiresAt) {
		t.Fatal("poll/cancel renewed phone idle lease")
	}
	c.Finish("fixture-job", false)
	c.Begin("failed-check")
	if err := c.Arm("failed-check", "goal", "Fixture", func() error { return errors.New("C:\\private\\user secret OS diagnostic") }); err != nil {
		t.Fatal(err)
	}
	c.Finish("failed-check", true)
	w = phoneRequest(h, "GET", host, "/api/host-power", "", cookie, true)
	snapshot := decodeWebHostPower(t, w)
	if snapshot.Status != "blocked" || snapshot.Error == "" || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("unsafe state: %s", w.Body.String())
	}
	if clock.timers() != 0 || clock.executed.Load() != 0 {
		t.Fatal("status armed or executed")
	}
}

func TestHostPowerLazyEngineAndClose(t *testing.T) {
	e := &Engine{}
	var wg sync.WaitGroup
	controllers := make(chan *systemhostpower.Controller, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); controllers <- e.hostPowerController() }()
	}
	wg.Wait()
	close(controllers)
	c := e.hostPowerController()
	for got := range controllers {
		if got != c {
			t.Fatal("multiple controllers")
		}
	}
	c.Begin("no-executor")
	if err := c.Arm("no-executor", "goal", "Fixture", func() error { return nil }); err == nil {
		t.Fatal("literal Engine got live authority")
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	empty := &Engine{}
	if err := empty.Close(); err != nil {
		t.Fatal(err)
	}
	if empty.hostPower != nil || empty.hostPowerController() != nil {
		t.Fatal("Close created controller")
	}
	fake := &Engine{}
	controller, clock := installWebHostPower(t, fake)
	armWebHostPower(t, controller)
	controller.Finish("fixture-job", true)
	if err := fake.Close(); err != nil {
		t.Fatal(err)
	}
	clock.fire()
	if controller.Snapshot().Status != "cancelled" || clock.executed.Load() != 0 {
		t.Fatal("Close left countdown alive")
	}
}

// This provider drives the real registry/ask_user path, then records native
// persisted goal verification. It has no model API or OS side effects.
type webHostPowerProvider struct {
	calls       atomic.Int32
	svc         *goal.Service
	goalID      string
	workEntered chan struct{}
	workRelease chan struct{}
}

func (*webHostPowerProvider) Name() string { return "host-power-fixture" }
func (p *webHostPowerProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	n := p.calls.Add(1)
	out := make(chan llm.Delta, 2)
	if n == 1 {
		args, _ := json.Marshal(map[string]string{"action": "arm", "goal_id": p.goalID})
		out <- llm.Delta{ToolCall: &llm.ToolCall{ID: "arm-fixture", Name: "host_power", Arguments: string(args)}}
		out <- llm.Delta{FinishReason: "tool_calls"}
	} else {
		if p.workEntered != nil {
			close(p.workEntered)
			select {
			case <-p.workRelease:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if err := p.svc.SetTaskStatus(ctx, p.goalID, 1, goal.TaskDone); err != nil {
			return nil, err
		}
		if err := p.svc.Verify(ctx, p.goalID, true, "Native fixture checks passed"); err != nil {
			return nil, err
		}
		if err := p.svc.SetStatus(ctx, p.goalID, goal.StatusDone); err != nil {
			return nil, err
		}
		out <- llm.Delta{Role: llm.RoleAssistant, Content: "The requested work result is ready."}
		out <- llm.Delta{FinishReason: "stop"}
	}
	close(out)
	return out, nil
}
func setupWebHostPowerRun(t *testing.T) (*Server, *systemhostpower.Controller, *webHostPowerClock) {
	t.Helper()
	s := newTestServer(t, false)
	controller, clock := installWebHostPower(t, s.eng)
	svc, err := s.eng.goalServiceAt(context.Background(), s.eng.Home())
	if err != nil {
		t.Fatal(err)
	}
	g, err := svc.Set(context.Background(), "Finish fixture work", "", "native fixture checks", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddTask(context.Background(), g.ID, "Implement and verify"); err != nil {
		t.Fatal(err)
	}
	s.eng.mu.Lock()
	s.eng.prov = &webHostPowerProvider{svc: svc, goalID: g.ID}
	s.eng.mu.Unlock()
	return s, controller, clock
}
func answerWebHostPower(t *testing.T, e *Engine, ev wireEvent) {
	t.Helper()
	if ev.Type != "question" {
		return
	}
	e.questionMu.Lock()
	req, ok := e.questions[ev.Question.ID]
	e.questionMu.Unlock()
	if !ok {
		t.Error("confirmation was not registered")
		return
	}
	req.Respond <- tools.AskAnswer{Selected: []string{"Allow once"}}
}

func TestHostPowerStreamFinalizesAfterCleanupAndSurvivesDisconnect(t *testing.T) {
	for _, cancelRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "disconnect", true: "stop"}[cancelRun], func(t *testing.T) {
			s, c, clock := setupWebHostPowerRun(t)
			sender, disconnect := context.WithCancel(context.Background())
			ctx, stop, finish := s.eng.beginChatRun(sender)
			defer finish()
			atDone := make(chan struct{})
			release := make(chan struct{})
			result := make(chan error, 1)
			var approvals atomic.Int32
			go func() {
				result <- s.eng.runStreamWithImages(ctx, "Perform fixture work", "", "", nil, func(ev wireEvent) {
					if ev.Type == "question" {
						approvals.Add(1)
						answerWebHostPower(t, s.eng, ev)
					}
					if ev.Type == "done" {
						close(atDone)
						<-release
					}
				})
			}()
			select {
			case <-atDone:
			case err := <-result:
				t.Fatalf("run ended before done: %v state=%+v", err, c.Snapshot())
			case <-time.After(10 * time.Second):
				t.Fatal("no terminal event")
			}
			if state := c.Snapshot(); state.Status != "armed" || clock.timers() != 0 {
				t.Fatalf("Done bypassed cleanup: %+v", state)
			}
			disconnect()
			if ctx.Err() != nil {
				t.Fatal("sender disconnect cancelled owned run")
			}
			if cancelRun {
				stop()
			}
			close(release)
			select {
			case err := <-result:
				if !cancelRun && err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("cleanup did not finish")
			}
			if approvals.Load() != 1 {
				t.Fatalf("confirmations=%d", approvals.Load())
			}
			want := "countdown"
			if cancelRun {
				want = "blocked"
			}
			if state := c.Snapshot(); state.Status != want {
				t.Fatalf("after cleanup: %+v want=%s", state, want)
			}
			if !cancelRun {
				w := httptest.NewRecorder()
				s.handleHostPower(w, httptest.NewRequest("GET", "/api/host-power?session=other-phone", nil))
				if state := decodeWebHostPower(t, w); state.Status != "countdown" {
					t.Fatal("disconnect lost shared countdown")
				}
			}
			clock.fire()
			wantExecuted := int32(1)
			if cancelRun {
				wantExecuted = 0
			}
			if clock.executed.Load() != wantExecuted {
				t.Fatalf("fake executions=%d want=%d", clock.executed.Load(), wantExecuted)
			}
		})
	}
}

func TestHostPowerPhoneHTTPDisconnectKeepsOwnedCountdown(t *testing.T) {
	s, controller, clock := setupWebHostPowerRun(t)
	setupTestPhone(t, s)
	cookie := pairTestPhone(t, s)
	s.eng.mu.Lock()
	provider := s.eng.prov.(*webHostPowerProvider)
	provider.workEntered = make(chan struct{})
	provider.workRelease = make(chan struct{})
	s.eng.mu.Unlock()
	var release sync.Once
	releaseWork := func() { release.Do(func() { close(provider.workRelease) }) }
	api := httptest.NewServer(s.Handler())
	defer api.Close()
	defer func() { s.eng.stopChatRuns(); releaseWork(); s.eng.chatLifetime.wg.Wait() }()
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodPost, api.URL+"/api/chat", strings.NewReader(`{"prompt":"Perform fixture work","turn_id":"host-power-phone"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "test-phone.trycloudflare.com"
	req.Header.Set("Origin", "https://"+req.Host)
	req.Header.Set("Cf-Ray", "test")
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.Status)
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	sid := ""
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev wireEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Type == "session" {
			sid = ev.SessionID
		}
		if ev.Type == "question" {
			answerWebHostPower(t, s.eng, ev)
			break
		}
	}
	if sid == "" {
		t.Fatalf("missing session: %v", scanner.Err())
	}
	select {
	case <-provider.workEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("arm did not complete")
	}
	if controller.Snapshot().Status != "armed" {
		t.Fatalf("not armed: %+v", controller.Snapshot())
	}
	// Close the real paired HTTP sender while the root is still working.
	response.Body.Close()
	releaseWork()
	finished := make(chan struct{})
	go func() { s.eng.chatLifetime.wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("disconnected owned run did not finish")
	}
	if state := controller.Snapshot(); state.Status != "countdown" {
		t.Fatalf("disconnect revoked successful work: %+v", state)
	}
	store, err := s.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	seq, err := store.LatestMessageSeq(context.Background(), sid, string(llm.RoleAssistant))
	if err != nil || seq <= 0 {
		t.Fatalf("countdown before durable assistant: seq=%d err=%v", seq, err)
	}
	w := phoneRequest(s.Handler(), "GET", req.Host, fmt.Sprintf("/api/host-power?session=%s", sid), "", cookie, true)
	if state := decodeWebHostPower(t, w); state.Status != "countdown" || state.Deadline == 0 {
		t.Fatalf("phone cannot poll retained countdown: %+v", state)
	}
	clock.fire()
	if clock.executed.Load() != 1 {
		t.Fatalf("fake executions=%d", clock.executed.Load())
	}
}

func TestHostPowerFeatureLoopsHaveNoCapability(t *testing.T) {
	s := newTestServer(t, false)
	installWebHostPower(t, s.eng)
	_, reg, err := s.eng.buildLoopWithSession(nil, nil, s.eng.Home(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("host_power"); ok {
		t.Fatal("feature loop exposes host power")
	}
	ask := make(chan tools.AskRequest, 1)
	_, reg, err = s.eng.buildLoopWithSession(nil, nil, s.eng.Home(), ask, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("host_power"); !ok {
		t.Fatal("real GUI loop missing host power")
	}
}
