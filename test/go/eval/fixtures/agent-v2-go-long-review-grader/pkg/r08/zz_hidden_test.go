package r08

import "testing"

func TestHiddenReview(t *testing.T) {
	if got := Dedupe([]string{"b", "a", "b", "c", "a"}); len(got) != 3 || got[0] != "b" || got[1] != "a" || got[2] != "c" {
		t.Fatalf("%q", got)
	}
}
