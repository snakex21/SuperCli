package webgui

import (
	"os"
	"path/filepath"
	"testing"
)

// background_wake_delegate is on by default, can be switched off from the
// settings panel, and a project config can switch it off under a global on.
func TestWakeDelegateKnobDefaultOffAndProjectOverride(t *testing.T) {
	srv := newTestServer(t, false)
	if !srv.eng.wakeConfig("").Delegate {
		t.Fatal("automatic turns must be allowed to start planned work by default")
	}
	if rec := knobsPOST(t, srv, `{"key":"background_wake_delegate","value":"off"}`); rec.Code != 200 {
		t.Fatalf("set off: %d %s", rec.Code, rec.Body.String())
	}
	if k := findKnob(t, knobsGET(t, srv), "background_wake_delegate"); k.Value != "off" || k.Source != "manual" {
		t.Fatalf("after set: %+v", k)
	}
	if srv.eng.wakeConfig("").Delegate {
		t.Fatal("the switch did not reach the wake scheduler")
	}
	if rec := knobsPOST(t, srv, `{"key":"background_wake_delegate","value":"on"}`); rec.Code != 200 {
		t.Fatalf("set on: %d %s", rec.Code, rec.Body.String())
	}
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".supercli"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".supercli", "config.toml"), []byte("background_wake_delegate = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if srv.eng.wakeConfig(project).Delegate {
		t.Fatal("a project config could not switch delegation off under a global on")
	}
}
