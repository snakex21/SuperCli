package r11

import "testing"

func TestVisible(t *testing.T) {
	if Env("R11_SURELY_UNSET", "d") != "d" {
		t.Fatal("env")
	}
}
