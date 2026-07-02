package webgui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/system/config"
)

// Regression tests for the "web GUI silently overwrites the CLI
// default model" bug: switching the model in the browser used to call
// SaveActiveConfig, which rewrote default_model/default_provider in
// the global config.toml — so a rate-limited model picked in the web
// GUI became the CLI default behind the user's back. The web GUI must
// keep its model selection in its own webgui-settings.json; only the
// explicit /api/model/default action may touch config.toml.

// seedConfigToml writes a config.toml with a known default_model into
// dataDir and returns its path and raw content.
func seedConfigToml(t *testing.T, dataDir string) (string, []byte) {
	t.Helper()
	path := filepath.Join(dataDir, "config.toml")
	content := []byte("default_model = \"cli-default-model\"\ndefault_provider = \"cli-provider\"\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("seed config.toml: %v", err)
	}
	return path, content
}

func TestSwitchModel_DoesNotTouchConfigToml(t *testing.T) {
	dir := t.TempDir()
	cfgPath, before := seedConfigToml(t, dir)
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	srv := NewServer(eng, false)

	req := httptest.NewRequest(http.MethodPost, "/api/model", strings.NewReader(`{"model":"echo-2","provider":""}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleModel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/model: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if eng.ModelName() != "echo-2" {
		t.Errorf("active model = %q, want echo-2", eng.ModelName())
	}

	// Global config.toml must be byte-identical.
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config.toml after switch: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("config.toml was modified by web model switch:\nbefore: %s\nafter:  %s", before, after)
	}
	tc, err := config.LoadToml(cfgPath)
	if err != nil {
		t.Fatalf("load config.toml: %v", err)
	}
	if tc.DefaultModel != "cli-default-model" {
		t.Errorf("default_model = %q, want cli-default-model (untouched)", tc.DefaultModel)
	}
	// No project-layer config may appear either (SaveActiveConfig used
	// to write both layers).
	if _, err := os.Stat(filepath.Join(dir, ".supercli", "config.toml")); !os.IsNotExist(err) {
		t.Errorf("project config.toml appeared after web model switch (err=%v)", err)
	}

	// The selection must land in the web GUI's own state instead.
	model, provider := LastModel(dir)
	if model != "echo-2" {
		t.Errorf("LastModel model = %q, want echo-2", model)
	}
	_ = provider // echo has no configured provider entry; empty is fine
}

func TestSwitchModel_PreservesOtherUISettings(t *testing.T) {
	dir := t.TempDir()
	// Existing settings blob written by the front-end must survive a
	// server-side last-model write.
	seed := []byte(`{"supercli-theme":"light","supercli-settings":"{\"lang\":\"pl\"}"}`)
	if err := os.WriteFile(filepath.Join(dir, uiSettingsFile), seed, 0o644); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := eng.SwitchModel("echo-3", ""); err != nil {
		t.Fatalf("SwitchModel: %v", err)
	}
	uiSettingsMu.Lock()
	blob := readUISettings(dir)
	uiSettingsMu.Unlock()
	if blob["supercli-theme"] != "light" {
		t.Errorf("supercli-theme = %v, want light (preserved)", blob["supercli-theme"])
	}
	if model, _ := LastModel(dir); model != "echo-3" {
		t.Errorf("LastModel = %q, want echo-3", model)
	}
}

func TestHandleModelDefault_WritesConfigToml(t *testing.T) {
	dir := t.TempDir()
	cfgPath, _ := seedConfigToml(t, dir)
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	srv := NewServer(eng, false)

	req := httptest.NewRequest(http.MethodPost, "/api/model/default", strings.NewReader(`{"model":"picked-model","provider":"cli-provider"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleModelDefault(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/model/default: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	tc, err := config.LoadToml(cfgPath)
	if err != nil {
		t.Fatalf("load config.toml: %v", err)
	}
	if tc.DefaultModel != "picked-model" {
		t.Errorf("default_model = %q, want picked-model (explicit action must persist)", tc.DefaultModel)
	}
}

func TestLastModel_MissingOrCorrupt(t *testing.T) {
	dir := t.TempDir()
	if m, p := LastModel(dir); m != "" || p != "" {
		t.Errorf("missing file: LastModel = (%q, %q), want empty", m, p)
	}
	if err := os.WriteFile(filepath.Join(dir, uiSettingsFile), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("seed corrupt: %v", err)
	}
	if m, p := LastModel(dir); m != "" || p != "" {
		t.Errorf("corrupt file: LastModel = (%q, %q), want empty", m, p)
	}
}
