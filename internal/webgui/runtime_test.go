package webgui

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeStatusAndLogExport(t *testing.T) {
	srv := newTestServer(t, false)
	status := httptest.NewRecorder()
	srv.handleRuntime(status, httptest.NewRequest(http.MethodGet, "/api/runtime", nil))
	if status.Code != http.StatusOK {
		t.Fatalf("runtime status=%d body=%s", status.Code, status.Body.String())
	}
	var payload struct {
		App             string `json:"app"`
		Status          string `json:"status"`
		Engine          string `json:"engine"`
		UIContract      int    `json:"ui_contract"`
		UpdateSupported bool   `json:"update_supported"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.App != "SuperCli" || payload.Status != "running" || payload.Engine != "SuperCli" || payload.UIContract != UIContractVersion || payload.UpdateSupported {
		t.Fatalf("runtime payload=%+v", payload)
	}

	logs := filepath.Join(srv.eng.DataDir(), "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, "supercli-web.log"), []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, "ignore.exe"), []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	exported := httptest.NewRecorder()
	srv.handleRuntimeLogs(exported, httptest.NewRequest(http.MethodGet, "/api/runtime/logs", nil))
	if exported.Code != http.StatusOK {
		t.Fatalf("log export=%d body=%s", exported.Code, exported.Body.String())
	}
	if disposition := exported.Header().Get("Content-Disposition"); !strings.Contains(disposition, "supercli-logs-") {
		t.Fatalf("log export filename = %q", disposition)
	}
	archive, err := zip.NewReader(bytes.NewReader(exported.Body.Bytes()), int64(exported.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.File) != 1 || archive.File[0].Name != "supercli-web.log" {
		t.Fatalf("archive files=%v", archive.File)
	}
}

func TestRuntimeUsesBrandedApplicationName(t *testing.T) {
	srv := newTestServer(t, false)
	srv.appName = "NestCafe"
	rec := httptest.NewRecorder()
	srv.handleRuntime(rec, httptest.NewRequest(http.MethodGet, "/api/runtime", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("runtime status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		App    string `json:"app"`
		Engine string `json:"engine"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.App != "NestCafe" || payload.Engine != "SuperCli" {
		t.Fatalf("runtime payload=%+v", payload)
	}
}
