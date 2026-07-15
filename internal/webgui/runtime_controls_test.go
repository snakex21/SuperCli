package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkersEndpointUsesSharedRegistry(t *testing.T) {
	srv := newTestServer(t, false)
	srv.eng.workers.Add("explore", "inspect project", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/workers", nil)
	req.Host = "localhost"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Workers []webWorker `json:"workers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Workers) != 1 || got.Workers[0].Agent != "explore" {
		t.Fatalf("workers=%+v", got.Workers)
	}
}

func TestWorkersEndpointRejectsUnknownStop(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodPost, "/api/workers", strings.NewReader(`{"id":"worker-404","action":"stop"}`))
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDoctorEndpointReturnsStructuredReport(t *testing.T) {
	srv := newTestServer(t, false)
	req := httptest.NewRequest(http.MethodGet, "/api/doctor", nil)
	req.Host = "localhost"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Checks  []map[string]any `json:"checks"`
		Summary map[string]int   `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Checks) == 0 || len(got.Summary) != 4 {
		t.Fatalf("doctor=%+v", got)
	}
}
