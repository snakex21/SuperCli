package rules

import "testing"

func TestHiddenRule137(t *testing.T) {
	if rule137(64) || !rule137(65) {
		t.Fatal("rule137 must use threshold 64")
	}
	if !rule037(42) || rule037(41) || !rule173(42) || rule173(41) {
		t.Fatal("rule037/rule173 must keep threshold 41")
	}
}
