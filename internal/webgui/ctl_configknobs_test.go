package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"supercli/internal/system/config"
	"supercli/internal/tools/sandbox"
)

// knobsGET fetches the panel and decodes the rows.
func knobsGET(t *testing.T, srv *Server) []knobView {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	srv.handleConfigKnobs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Knobs []knobView `json:"knobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Knobs
}

func knobsPOST(t *testing.T, srv *Server, payload string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	srv.handleConfigKnobs(rec, req)
	return rec
}

func findKnob(t *testing.T, knobs []knobView, key string) knobView {
	t.Helper()
	for _, k := range knobs {
		if k.Key == key {
			return k
		}
	}
	t.Fatalf("knob %q not in response", key)
	return knobView{}
}

func TestConfigKnobs_DefaultsMirrorTUI(t *testing.T) {
	srv := newTestServer(t, false)
	knobs := knobsGET(t, srv)

	// Same keys, same order as the TUI settingsRows() (minus reset-all).
	wantOrder := []string{
		"orchestrator", "allow_all", "thinking", "navigator", "stable_toolset",
		"cache_prompt", "darwin_parallel", "task_parallel",
		"memory_briefing_tokens", "task_max_steps", "task_max_tokens",
		"task_model", "compact_model", "fallback_models", "fallback_cooldown_seconds",
		"noop_gate", "preflight_repo", "draft_verify",
		"draft_verify_max_rounds", "verify_commands",
		"default_model", "default_provider",
	}
	if len(knobs) != len(wantOrder) {
		t.Fatalf("knob count = %d, want %d", len(knobs), len(wantOrder))
	}
	for i, key := range wantOrder {
		if knobs[i].Key != key {
			t.Errorf("knob[%d] = %q, want %q", i, knobs[i].Key, key)
		}
	}

	if k := findKnob(t, knobs, "orchestrator"); k.Value != "auto" || k.Source != "default" || k.Default != "auto" {
		t.Errorf("orchestrator default: %+v", k)
	}
	if k := findKnob(t, knobs, "allow_all"); k.Value != "off" || k.Source != "default" {
		t.Errorf("allow_all default: %+v", k)
	}
	if k := findKnob(t, knobs, "preflight_repo"); k.Value != "on" || k.Source != "default" {
		t.Errorf("preflight_repo default: %+v", k)
	}
	if k := findKnob(t, knobs, "thinking"); k.Default != "on" {
		t.Errorf("thinking reset target should be explicit: %+v", k)
	}
	if k := findKnob(t, knobs, "draft_verify"); k.Default != "off" {
		t.Errorf("draft_verify reset target should be explicit: %+v", k)
	}
	if k := findKnob(t, knobs, "cache_prompt"); k.Value != "auto" || k.Kind != knobTriAuto {
		t.Errorf("cache_prompt default: %+v", k)
	}
	if k := findKnob(t, knobs, "default_model"); k.Kind != knobReadonly {
		t.Errorf("default_model should be readonly: %+v", k)
	}
}

func TestConfigKnobs_SetAndReset(t *testing.T) {
	srv := newTestServer(t, false)
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(false) })

	if rec := knobsPOST(t, srv, `{"key":"orchestrator","value":"on"}`); rec.Code != http.StatusOK {
		t.Fatalf("set orchestrator: %d %s", rec.Code, rec.Body.String())
	}
	if k := findKnob(t, knobsGET(t, srv), "orchestrator"); k.Value != "on" || k.Source != "manual" {
		t.Errorf("after set: %+v", k)
	}
	// Persisted to the global config.toml, not just in memory.
	global, _ := config.FindTomlPaths(srv.eng.DataDir(), srv.eng.Home())
	tc, err := config.LoadToml(global)
	if err != nil || tc.Orchestrator == nil || !*tc.Orchestrator {
		t.Errorf("config.toml orchestrator = %v, err %v", tc.Orchestrator, err)
	}

	if rec := knobsPOST(t, srv, `{"key":"orchestrator","value":"default"}`); rec.Code != http.StatusOK {
		t.Fatalf("reset orchestrator: %d", rec.Code)
	}
	if k := findKnob(t, knobsGET(t, srv), "orchestrator"); k.Value != "auto" || k.Source != "default" {
		t.Errorf("after reset: %+v", k)
	}

	if rec := knobsPOST(t, srv, `{"key":"allow_all","value":"on"}`); rec.Code != http.StatusOK {
		t.Fatalf("set allow_all: %d %s", rec.Code, rec.Body.String())
	}
	if !sandbox.IsUnsandboxed() {
		t.Error("allow_all did not update the live sandbox state")
	}
	if rec := knobsPOST(t, srv, `{"key":"allow_all","value":"off"}`); rec.Code != http.StatusOK {
		t.Fatalf("reset allow_all: %d %s", rec.Code, rec.Body.String())
	}
	if sandbox.IsUnsandboxed() {
		t.Error("disabling allow_all left the live sandbox open")
	}
}

func TestConfigKnobs_IntAndText(t *testing.T) {
	srv := newTestServer(t, false)

	if rec := knobsPOST(t, srv, `{"key":"task_max_steps","value":"12"}`); rec.Code != http.StatusOK {
		t.Fatalf("set int: %d %s", rec.Code, rec.Body.String())
	}
	if k := findKnob(t, knobsGET(t, srv), "task_max_steps"); k.Value != "12" || k.Raw != "12" {
		t.Errorf("int knob: %+v", k)
	}
	if rec := knobsPOST(t, srv, `{"key":"task_max_steps","value":"abc"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad int accepted: %d", rec.Code)
	}

	if rec := knobsPOST(t, srv, `{"key":"verify_commands","value":"go build ./... ; go test ./..."}`); rec.Code != http.StatusOK {
		t.Fatalf("set text: %d", rec.Code)
	}
	if k := findKnob(t, knobsGET(t, srv), "verify_commands"); !strings.Contains(k.Value, "go build") || k.Source != "manual" {
		t.Errorf("text knob: %+v", k)
	}
	// Empty clears back to default.
	if rec := knobsPOST(t, srv, `{"key":"verify_commands","value":""}`); rec.Code != http.StatusOK {
		t.Fatalf("clear text: %d", rec.Code)
	}
	if k := findKnob(t, knobsGET(t, srv), "verify_commands"); k.Source != "default" {
		t.Errorf("text knob after clear: %+v", k)
	}

	if rec := knobsPOST(t, srv, `{"key":"fallback_models","value":"hp/qwen ; cloud/gpt"}`); rec.Code != http.StatusOK {
		t.Fatalf("set fallback list: %d", rec.Code)
	}
	if k := findKnob(t, knobsGET(t, srv), "fallback_models"); k.Raw != "hp/qwen ; cloud/gpt" || k.Source != "manual" {
		t.Errorf("fallback knob: %+v", k)
	}

	if rec := knobsPOST(t, srv, `{"key":"compact_model","value":"cheap/qwen"}`); rec.Code != http.StatusOK {
		t.Fatalf("set compact model: %d", rec.Code)
	}
	if k := findKnob(t, knobsGET(t, srv), "compact_model"); k.Raw != "cheap/qwen" || k.Source != "manual" {
		t.Errorf("compact model knob: %+v", k)
	}
}

func TestConfigKnobs_ResetAll(t *testing.T) {
	srv := newTestServer(t, false)
	knobsPOST(t, srv, `{"key":"draft_verify","value":"on"}`)
	knobsPOST(t, srv, `{"key":"task_model","value":"qwen"}`)
	if rec := knobsPOST(t, srv, `{"reset_all":true}`); rec.Code != http.StatusOK {
		t.Fatalf("reset all: %d", rec.Code)
	}
	for _, k := range knobsGET(t, srv) {
		if k.Kind == knobReadonly {
			continue
		}
		if k.Source != "default" {
			t.Errorf("%s not reset: %+v", k.Key, k)
		}
	}
}

func TestConfigKnobs_UnknownKey(t *testing.T) {
	srv := newTestServer(t, false)
	if rec := knobsPOST(t, srv, `{"key":"default_model","value":"x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("readonly key accepted: %d", rec.Code)
	}
	if rec := knobsPOST(t, srv, `{"key":"nope","value":"x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown key accepted: %d", rec.Code)
	}
}
