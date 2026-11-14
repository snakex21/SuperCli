package r11

import "testing"

func TestHiddenReview(t *testing.T) {
	t.Setenv("R11_EMPTY", "")
	t.Setenv("R11_SET", "x")
	if Env("R11_EMPTY", "d") != "d" || Env("R11_SET", "d") != "x" || Env("R11_UNSET_ZZ", "d") != "d" {
		t.Fatal("Env")
	}
}
