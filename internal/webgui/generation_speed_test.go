package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"supercli/internal/agent"
	"supercli/internal/system/config"
	"testing"
	"time"
)

func TestGenerationSpeedSettingSharedPortableAndLive(t *testing.T) {
	srv := newTestServer(t, false)
	path := filepath.Join(srv.eng.DataDir(), "config.toml")
	read := func() bool {
		rec := httptest.NewRecorder()
		srv.handleUISettings(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
		if rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
		var body struct{ Settings map[string]any }
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		v, ok := body.Settings["ui.showGenerationSpeed"].(bool)
		if !ok {
			t.Fatal("missing boolean presentation setting")
		}
		return v
	}
	if !read() {
		t.Fatal("default should display speed")
	}
	for _, step := range []struct {
		value string
		want  bool
	}{{"off", false}, {"on", true}, {"default", true}} {
		rec := knobsPOST(t, srv, `{"key":"show_generation_speed","value":"`+step.value+`"}`)
		if rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
		if read() != step.want {
			t.Fatal("GUI startup disagrees with shared config")
		}
		cfg, err := config.LoadToml(path)
		if err != nil {
			t.Fatal(err)
		}
		if (cfg.ShowGenerationSpeed == nil || *cfg.ShowGenerationSpeed) != step.want {
			t.Fatal("portable config disagrees")
		}
		knob := findKnob(t, knobsGET(t, srv), "show_generation_speed")
		if knob.NextSession || knob.Default != "on" {
			t.Fatal("setting should be live/default-on")
		}
	}
	cfg, err := config.LoadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	off := false
	cfg.ShowGenerationSpeed = &off
	if err := config.SaveToml(path, cfg); err != nil {
		t.Fatal(err)
	}
	if read() {
		t.Fatal("external TUI config update ignored")
	}
}
func TestWireGenerationRateOmitsUnknownAndUsesStreamWindow(t *testing.T) {
	for _, tc := range []struct {
		event agent.DoneEvent
		want  float64
	}{{agent.DoneEvent{GenerationTokens: 120, GenerationDuration: 3 * time.Second}, 40}, {agent.DoneEvent{Usage: agent.Usage{Output: 120}}, 0}} {
		w, keep := toWireEvent(tc.event)
		if !keep || w.GenerationTPS != tc.want {
			t.Fatalf("wire=%+v", w)
		}
		raw, err := json.Marshal(w)
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]any
		_ = json.Unmarshal(raw, &data)
		_, exists := data["generation_tps"]
		if exists != (tc.want > 0) {
			t.Fatal("unknown rate not omitted")
		}
	}
}
