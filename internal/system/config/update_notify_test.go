package config

import "testing"

func TestUpdateNotifyDefaultOnAndMerge(t *testing.T) {
	if !UpdateNotifyEnabled(TomlConfig{}) {
		t.Fatal("update_notify must default to on")
	}
	off, on := false, true
	dst := TomlConfig{UpdateNotify: &off}
	mergeToml(&dst, TomlConfig{})
	if UpdateNotifyEnabled(dst) {
		t.Fatal("nil source cleared an explicit off")
	}
	mergeToml(&dst, TomlConfig{UpdateNotify: &on})
	if !UpdateNotifyEnabled(dst) {
		t.Fatal("explicit on did not override")
	}
}
