package webgui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supercli/internal/system/config"
)

func TestHandleOrchestrator_GetDefaultUnset(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	srv := NewServer(eng, false)

	rec := httptest.NewRecorder()
	srv.handleOrchestrator(rec, httptest.NewRequest(http.MethodGet, "/api/orchestrator", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"set":false`) {
		t.Errorf("unset default should report set=false, got %s", rec.Body.String())
	}
}

func TestHandleOrchestrator_PostPersists(t *testing.T) {
	dir := t.TempDir()
	cfgPath, _ := seedConfigToml(t, dir)
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	srv := NewServer(eng, false)

	// Enable.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/orchestrator", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	srv.handleOrchestrator(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body = %s", rec.Code, rec.Body.String())
	}
	tc, err := config.LoadToml(cfgPath)
	if err != nil {
		t.Fatalf("load config.toml: %v", err)
	}
	if tc.Orchestrator == nil || !*tc.Orchestrator {
		t.Fatalf("orchestrator not persisted true: %v", tc.Orchestrator)
	}

	// GET now reflects it.
	rec2 := httptest.NewRecorder()
	srv.handleOrchestrator(rec2, httptest.NewRequest(http.MethodGet, "/api/orchestrator", nil))
	if !strings.Contains(rec2.Body.String(), `"enabled":true`) || !strings.Contains(rec2.Body.String(), `"set":true`) {
		t.Errorf("GET after enable = %s", rec2.Body.String())
	}

	// Disable.
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/api/orchestrator", strings.NewReader(`{"enabled":false}`))
	req3.Header.Set("Content-Type", "application/json")
	srv.handleOrchestrator(rec3, req3)
	tc2, _ := config.LoadToml(cfgPath)
	if tc2.Orchestrator == nil || *tc2.Orchestrator {
		t.Fatalf("orchestrator not persisted false: %v", tc2.Orchestrator)
	}
}
