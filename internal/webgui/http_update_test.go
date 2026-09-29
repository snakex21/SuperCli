package webgui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"supercli/internal/agent"
	"supercli/internal/system/updater"
	"testing"
)

type fakeApplicationUpdater struct {
	calls []string
	err   error
}

func (f *fakeApplicationUpdater) call(action string) (updater.State, error) {
	f.calls = append(f.calls, action)
	return updater.State{CurrentVersion: "1.0.0", LatestVersion: "1.0.1", Status: action}, f.err
}
func (f *fakeApplicationUpdater) Check(context.Context) (updater.State, error) {
	return f.call("check")
}
func (f *fakeApplicationUpdater) Download(context.Context) (updater.State, error) {
	return f.call("download")
}
func (f *fakeApplicationUpdater) Install(context.Context) (updater.State, error) {
	return f.call("install")
}
func testUpdateServer() (*Server, *fakeApplicationUpdater) {
	fake := &fakeApplicationUpdater{}
	return &Server{eng: &Engine{}, updateFactory: func() (applicationUpdater, error) { return fake, nil }}, fake
}
func TestUpdateHTTPActionsAndErrors(t *testing.T) {
	srv, fake := testUpdateServer()
	for _, action := range []string{"check", "download", "install"} {
		method, body := http.MethodPost, `{"action":"`+action+`"}`
		if action == "check" {
			method, body = http.MethodGet, ""
		}
		rec := httptest.NewRecorder()
		srv.handleUpdate(rec, httptest.NewRequest(method, "/api/update", strings.NewReader(body)))
		if rec.Code != 200 || len(fake.calls) != 1 || fake.calls[0] != action || !strings.Contains(rec.Body.String(), `"current_version":"1.0.0"`) {
			t.Fatalf("%s: %d %s calls=%v", action, rec.Code, rec.Body.String(), fake.calls)
		}
		fake.calls = nil
	}
	fake.err = errors.New("checksum mismatch")
	rec := httptest.NewRecorder()
	srv.handleUpdate(rec, httptest.NewRequest(http.MethodPost, "/api/update", strings.NewReader(`{"action":"download"}`)))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "update.downloadFailed") {
		t.Fatalf("failure: %d %s", rec.Code, rec.Body.String())
	}
}
func TestUpdateHTTPRejectsMalformedRequestBeforeAnyOperation(t *testing.T) {
	srv, fake := testUpdateServer()
	for _, body := range []string{`{"action":"erase"}`, `{"action":"install","extra":true}`, `{"action":"install"}{}`, strings.Repeat("x", 2048), "{"} {
		rec := httptest.NewRecorder()
		srv.handleUpdate(rec, httptest.NewRequest(http.MethodPost, "/api/update", strings.NewReader(body)))
		if rec.Code != 400 || len(fake.calls) != 0 {
			t.Fatalf("malformed body reached updater: %d %v", rec.Code, fake.calls)
		}
	}
	rec := httptest.NewRecorder()
	srv.handleUpdate(rec, httptest.NewRequest(http.MethodDelete, "/api/update", nil))
	if rec.Code != 405 || len(fake.calls) != 0 {
		t.Fatal("DELETE reached updater")
	}
}
func TestUpdateInstallWaitsForIdleForegroundAndWorkers(t *testing.T) {
	srv, fake := testUpdateServer()
	install := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.handleUpdate(rec, httptest.NewRequest(http.MethodPost, "/api/update", strings.NewReader(`{"action":"install"}`)))
		return rec
	}
	finish := srv.eng.beginActiveRun()
	if rec := install(); rec.Code != 409 || len(fake.calls) != 0 {
		t.Fatal("active foreground installation permitted")
	}
	finish()
	finish()
	srv.eng.workers = agent.NewWorkerRegistry()
	srv.eng.workers.Add("worker", "pending", nil)
	if rec := install(); rec.Code != 409 || len(fake.calls) != 0 {
		t.Fatal("active worker installation permitted")
	}
	srv.eng.workers = nil
	if rec := install(); rec.Code != 200 || len(fake.calls) != 1 {
		t.Fatalf("idle install refused: %d %s", rec.Code, rec.Body.String())
	}
}
func TestUpdateHTTPRefusesOtherBrandsAndRemoteAuthenticatedPeer(t *testing.T) {
	srv, fake := testUpdateServer()
	srv.appName = "NestCafe"
	rec := httptest.NewRecorder()
	srv.handleUpdate(rec, httptest.NewRequest(http.MethodGet, "/api/update", nil))
	if rec.Code != 400 || len(fake.calls) != 0 {
		t.Fatal("branded launcher uses SuperCli update")
	}
	remote := newTestServer(t, true)
	remote.updateFactory = func() (applicationUpdater, error) { return fake, nil }
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765/api/update", strings.NewReader(`{"action":"install"}`))
	req.RemoteAddr = "203.0.113.8:4321"
	req.Header.Set("Authorization", "Bearer "+remote.sessionToken)
	rec = httptest.NewRecorder()
	remote.Handler().ServeHTTP(rec, req)
	if rec.Code != 403 || len(fake.calls) != 0 {
		t.Fatal("authenticated remote request can install application binaries")
	}
}
