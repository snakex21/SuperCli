package webgui

import (
	"net/http"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func TestConfigKnobs_RetrySettings(t *testing.T) {
	t.Cleanup(func() { config.ApplyRetryGlobals(config.TomlConfig{}) })
	srv := newTestServer(t, false)

	if k := findKnob(t, knobsGET(t, srv), "max_retries"); k.Value != "default (5)" || k.Source != "default" || k.Kind != knobText {
		t.Fatalf("default max_retries knob: %+v", k)
	}
	for in, want := range map[string]struct {
		value string
		n     int
	}{"7": {"7", 7}, "0": {"0", 0}, "inf": {"∞", -1}, "-1": {"∞", -1}} {
		if rec := knobsPOST(t, srv, `{"key":"max_retries","value":"`+in+`"}`); rec.Code != http.StatusOK {
			t.Fatalf("set %q: %d %s", in, rec.Code, rec.Body.String())
		}
		k := findKnob(t, knobsGET(t, srv), "max_retries")
		if k.Value != want.value || k.Source != "manual" || llm.MaxRetries() != want.n {
			t.Fatalf("%q: knob %+v applied %d", in, k, llm.MaxRetries())
		}
	}
	for _, bad := range []string{"-5", "abc"} {
		if rec := knobsPOST(t, srv, `{"key":"max_retries","value":"`+bad+`"}`); rec.Code != http.StatusBadRequest {
			t.Fatalf("bad %q accepted: %d", bad, rec.Code)
		}
	}
	if rec := knobsPOST(t, srv, `{"key":"max_retries","value":""}`); rec.Code != http.StatusOK || llm.MaxRetries() != llm.DefaultMaxRetries {
		t.Fatalf("reset: %d %d", rec.Code, llm.MaxRetries())
	}

	if rec := knobsPOST(t, srv, `{"key":"retry_unknown_errors","value":"on"}`); rec.Code != http.StatusOK || !llm.RetryUnknownErrors() {
		t.Fatalf("retry_unknown_errors on: %d", rec.Code)
	}
	if k := findKnob(t, knobsGET(t, srv), "retry_unknown_errors"); k.State != "on" || k.Default != "off" {
		t.Fatalf("retry_unknown_errors knob: %+v", k)
	}
	if rec := knobsPOST(t, srv, `{"key":"requests_per_minute","value":"40"}`); rec.Code != http.StatusOK || llm.RequestsPerMinuteFor("https://x.example") != 40 {
		t.Fatalf("requests_per_minute: %d", rec.Code)
	}
	if k := findKnob(t, knobsGET(t, srv), "requests_per_minute"); k.Value != "40" || k.Raw != "40" {
		t.Fatalf("requests_per_minute knob: %+v", k)
	}
}
