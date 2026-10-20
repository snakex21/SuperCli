package webgui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	systemhostpower "supercli/internal/system/hostpower"
)

func assertHostPowerAdmissionBlocksArm(t *testing.T, controller *systemhostpower.Controller) {
	t.Helper()
	controller.Begin("other-root")
	defer controller.Finish("other-root", false)
	if err := controller.Arm("other-root", "other-goal", "Other verified work", func() error { return nil }); err == nil {
		controller.Cancel()
		t.Fatal("another root armed while web admission was pending")
	}
}

func assertHostPowerAdmissionReleased(t *testing.T, controller *systemhostpower.Controller) {
	t.Helper()
	controller.Begin("sole-root-probe")
	defer controller.Finish("sole-root-probe", false)
	if err := controller.Arm("sole-root-probe", "probe-goal", "Sole root probe", func() error { return nil }); err != nil {
		t.Fatalf("web admission leaked an active root: %v", err)
	}
	controller.Cancel()
}

func TestHostPowerHTTPAdmissionRevokesCountdownBeforeLoopStartup(t *testing.T) {
	s := newTestServer(t, true)
	controller, clock := installWebHostPower(t, s.eng)
	armWebHostPower(t, controller)
	controller.Finish("fixture-job", true)
	if controller.Snapshot().Status != "countdown" {
		t.Fatal("missing old countdown")
	}
	clock.mu.Lock()
	clock.now = clock.now.Add(59 * time.Second)
	clock.mu.Unlock()

	// Hold real loop construction, not a provider call: Loop.Run has not begun.
	// HTTP headers are flushed only after beginChatRun admits the trusted root.
	s.eng.goalMu.Lock()
	var unlockOnce sync.Once
	unblock := func() { unlockOnce.Do(s.eng.goalMu.Unlock) }
	defer unblock()
	api := httptest.NewServer(s.Handler())
	defer api.Close()
	defer func() { s.eng.stopChatRuns(); unblock(); s.eng.chatLifetime.wg.Wait() }()
	request, err := http.NewRequest(http.MethodPost, api.URL+"/api/chat", strings.NewReader(`{"prompt":"hello","turn_id":"admission-regression"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+s.sessionToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.Status)
	}
	if state := controller.Snapshot(); state.Status != "cancelled" || state.Deadline != 0 {
		t.Fatalf("HTTP admission left old countdown authorized before Loop.Run: %+v", state)
	}
	// Deliver the old callback even though timer cancellation has already run.
	clock.fire()
	if clock.executed.Load() != 0 {
		t.Fatal("old countdown executed during admitted startup")
	}
	assertHostPowerAdmissionBlocksArm(t, controller)
	unblock()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	s.eng.chatLifetime.wg.Wait()
	assertHostPowerAdmissionReleased(t, controller)
	if clock.executed.Load() != 0 {
		t.Fatal("startup or cleanup executed host power")
	}
}

func TestHostPowerDirectStreamAdmissionBeforeLoopStartup(t *testing.T) {
	s := newTestServer(t, false)
	controller, clock := installWebHostPower(t, s.eng)
	armWebHostPower(t, controller)
	controller.Finish("fixture-job", true)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		result <- s.eng.runStream(context.Background(), "hello", "", "", func(event wireEvent) {
			// Session announcement precedes loop construction and Loop.Run.
			if event.Type == "session" {
				close(entered)
				<-release
			}
		})
	}()
	defer func() {
		unblock()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("direct stream cleanup timed out")
		}
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("stream did not reach startup")
	}
	if state := controller.Snapshot(); state.Status != "cancelled" {
		t.Fatalf("direct admission did not revoke: %+v", state)
	}
	clock.fire()
	if clock.executed.Load() != 0 {
		t.Fatal("old deadline executed during direct stream startup")
	}
	assertHostPowerAdmissionBlocksArm(t, controller)
	unblock()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("direct stream did not finish")
	}
	assertHostPowerAdmissionReleased(t, controller)
}

func TestHostPowerAdmissionEarlyErrorReleasesBeforeOwnerFinish(t *testing.T) {
	s := newTestServer(t, false)
	controller, _ := installWebHostPower(t, s.eng)
	ctx, _, finish := s.eng.beginChatRun(context.Background())
	defer finish()
	assertHostPowerAdmissionBlocksArm(t, controller)
	if err := s.eng.runStream(ctx, "hello", "missing-session", "", func(wireEvent) {}); err == nil {
		t.Fatal("expected early session startup failure")
	}
	// The outer stream defer must release even before the HTTP owner finishes.
	assertHostPowerAdmissionReleased(t, controller)
	finish()
	finish()
	assertHostPowerAdmissionReleased(t, controller)
}

func TestHostPowerAdmissionOwnerFailureWithoutStreamReleases(t *testing.T) {
	e := &Engine{}
	controller, _ := installWebHostPower(t, e)
	_, _, finish := e.beginChatRun(context.Background())
	assertHostPowerAdmissionBlocksArm(t, controller)
	// HTTP completion/rewind setup may fail without ever entering the stream.
	finish()
	finish()
	assertHostPowerAdmissionReleased(t, controller)
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
}
