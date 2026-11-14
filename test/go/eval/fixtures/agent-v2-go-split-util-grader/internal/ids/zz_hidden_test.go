package ids

import "testing"

func TestHiddenIDs(t *testing.T) {
	a, b := New("x"), New("x")
	if a == b || len(a) != len("x_000001") {
		t.Fatal(a, b)
	}
}
