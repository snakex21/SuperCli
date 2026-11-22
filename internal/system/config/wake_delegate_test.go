package config

import "testing"

// background_wake_delegate is on unless the user turns it off, and an explicit
// off survives merging layers that leave it unset.
func TestWakeDelegateDefaultsOnAndMerge(t *testing.T) {
	if !(&TomlConfig{}).WakeDelegate() {
		t.Fatal("background_wake_delegate must default to on")
	}
	off := false
	dst := TomlConfig{}
	mergeToml(&dst, TomlConfig{BackgroundWakeDelegate: &off})
	if dst.WakeDelegate() {
		t.Fatal("explicit off did not merge")
	}
	mergeToml(&dst, TomlConfig{})
	if dst.WakeDelegate() {
		t.Fatal("unset source cleared an explicit off")
	}
}
