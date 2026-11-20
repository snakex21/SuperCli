package webgui

import (
	"net/http"
	"testing"
)

// 1.0.37: "Max. parallel agents" (max_parallel_agents) and its per-provider
// variant are settings; the engine applies them to the worker registry.
func TestParallelAgentsKnobs(t *testing.T) {
	srv := newTestServer(t, false)
	if k := findKnob(t, knobsGET(t, srv), "max_parallel_agents"); k.Value != "default (6)" || k.Source != "default" || k.Default != "6" {
		t.Fatalf("default knob = %+v", k)
	}
	if rec := knobsPOST(t, srv, `{"key":"max_parallel_agents","value":"bez limitu"}`); rec.Code != http.StatusOK {
		t.Fatalf("set unlimited: %d %s", rec.Code, rec.Body.String())
	}
	if k := findKnob(t, knobsGET(t, srv), "max_parallel_agents"); k.Value != "unlimited" || k.Raw != "0" || k.Source != "manual" {
		t.Fatalf("unlimited knob = %+v", k)
	}
	if rec := knobsPOST(t, srv, `{"key":"max_parallel_agents","value":"many"}`); rec.Code == http.StatusOK {
		t.Fatal("invalid value accepted")
	}
	if rec := knobsPOST(t, srv, `{"key":"max_parallel_agents_per_provider","value":"lmstudio=1; openrouter=4"}`); rec.Code != http.StatusOK {
		t.Fatalf("set per provider: %d %s", rec.Code, rec.Body.String())
	}
	if k := findKnob(t, knobsGET(t, srv), "max_parallel_agents_per_provider"); k.Value != "lmstudio=1; openrouter=4" {
		t.Fatalf("per-provider knob = %+v", k)
	}
	// The next run's wiring applies the settings to the shared registry.
	if _, err := srv.eng.newLoop(); err != nil {
		t.Fatal(err)
	}
	max, per := srv.eng.workers.ConcurrencyLimits()
	if max != 0 || per["lmstudio"] != 1 || per["openrouter"] != 4 {
		t.Fatalf("registry limits = %d %v", max, per)
	}
	if rec := knobsPOST(t, srv, `{"key":"max_parallel_agents","value":""}`); rec.Code != http.StatusOK {
		t.Fatal("reset failed")
	}
	if _, err := srv.eng.newLoop(); err != nil {
		t.Fatal(err)
	}
	if max, _ := srv.eng.workers.ConcurrencyLimits(); max != 6 {
		t.Fatalf("reset cap = %d", max)
	}
}
