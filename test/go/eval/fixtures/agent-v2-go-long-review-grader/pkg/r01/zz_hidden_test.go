package r01

import "testing"

func TestHiddenReview(t *testing.T) {
	if Clamp(5, 10, 0) != 5 || Clamp(-1, 10, 0) != 0 || Clamp(11, 10, 0) != 10 || Clamp(3, 0, 2) != 2 {
		t.Fatal("Clamp")
	}
}
