package webgui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"supercli/internal/system/updater"
)

type noticeUpdater struct{ calls int }

func (f *noticeUpdater) Check(context.Context) (updater.State, error) {
	f.calls++
	return updater.State{CurrentVersion: "1.0.0", LatestVersion: "9.9.9", Available: true, Supported: true, Status: "available"}, nil
}
func (f *noticeUpdater) Download(context.Context) (updater.State, error) { return updater.State{}, nil }
func (f *noticeUpdater) Install(context.Context) (updater.State, error)  { return updater.State{}, nil }

func TestUpdateNoticeDefaultOnCachedAndOptOut(t *testing.T) {
	srv := newTestServer(t, false)
	fake := &noticeUpdater{}
	srv.updateFactory = func() (applicationUpdater, error) { return fake, nil }
	get := func() map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.handleUpdateNotice(rec, httptest.NewRequest(http.MethodGet, "/api/update/notice", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d %s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := get(); got["available"] != true || got["latest_version"] != "9.9.9" {
		t.Fatalf("default-on notice: %v", got)
	}
	get()
	if fake.calls != 1 {
		t.Fatalf("daily result not reused: calls=%d", fake.calls)
	}
	if k := findKnob(t, knobsGET(t, srv), "update_notify"); k.Value != "on" || k.Source != "default" {
		t.Fatalf("update_notify knob default: %+v", k)
	}
	if rec := knobsPOST(t, srv, `{"key":"update_notify","value":"off"}`); rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body.String())
	}
	if got := get(); got["available"] != false || got["status"] != "disabled" {
		t.Fatalf("opt-out ignored: %v", got)
	}
	if fake.calls != 1 {
		t.Fatalf("disabled notice still contacted the updater: calls=%d", fake.calls)
	}
	rec := httptest.NewRecorder()
	srv.handleUpdateNotice(rec, httptest.NewRequest(http.MethodPost, "/api/update/notice", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST accepted: %d", rec.Code)
	}
}
