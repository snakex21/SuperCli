package r01

import "testing"

func TestVisible(t *testing.T) {
	if Clamp(5, 0, 10) != 5 {
		t.Fatal("clamp")
	}
}
