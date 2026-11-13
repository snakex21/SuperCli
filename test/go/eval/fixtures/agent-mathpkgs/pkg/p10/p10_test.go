package p10

import "testing"

func TestClamp(t *testing.T) {
	if Clamp(-5, 0, 10) != 0 || Clamp(50, 0, 10) != 10 || Clamp(5, 0, 10) != 5 {
		t.Fatal("clamp")
	}
}
