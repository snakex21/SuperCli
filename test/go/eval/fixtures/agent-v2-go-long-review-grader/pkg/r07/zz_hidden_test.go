package r07

import "testing"

func TestHiddenReview(t *testing.T) {
	if Percent(1, 8) != 13 || Percent(1, 3) != 33 || Percent(2, 3) != 67 || Percent(5, 0) != 0 || Percent(1, 2) != 50 {
		t.Fatal("Percent")
	}
}
