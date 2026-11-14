package r06

import "testing"

func TestHiddenReview(t *testing.T) {
	if got := SplitFields(" a, b ,c "); len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("%q", got)
	}
}
