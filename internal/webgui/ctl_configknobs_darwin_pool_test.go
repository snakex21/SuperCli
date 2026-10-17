package webgui

import (
	"net/http"
	"testing"

	"supercli/internal/system/config"
)

func TestDarwinPoolSizeKnob(t *testing.T) {
	srv := newTestServer(t, false)
	row := findKnob(t, knobsGET(t, srv), "darwin_pool_size")
	if row.Raw != "" || row.Source != "default" {
		t.Fatalf("default row = %+v", row)
	}
	for _, tc := range []struct{ in, want string }{{"1", "1"}, {"10", "10"}, {"auto", ""}, {"5", "5"}, {"", ""}, {"0", ""}} {
		rec := knobsPOST(t, srv, `{"key":"darwin_pool_size","value":"`+tc.in+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("save %q: %d %s", tc.in, rec.Code, rec.Body.String())
		}
		if got := findKnob(t, knobsGET(t, srv), "darwin_pool_size").Raw; got != tc.want {
			t.Fatalf("after %q raw=%q want %q", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"11", "-1", "many"} {
		if rec := knobsPOST(t, srv, `{"key":"darwin_pool_size","value":"`+bad+`"}`); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid %q accepted: %d", bad, rec.Code)
		}
	}
	c := config.TomlConfig{DarwinPoolSize: 7}
	knobResetAll(&c)
	if c.DarwinPoolSize != 0 {
		t.Fatalf("reset all kept darwin_pool_size=%d", c.DarwinPoolSize)
	}
}
