package r03

import "testing"

func TestHiddenReview(t *testing.T) {
	if _, err := Mean(nil); err != ErrEmpty {
		t.Fatal("Mean(nil)", err)
	}
	if m, err := Mean([]float64{1, 2}); err != nil || m != 1.5 {
		t.Fatal("Mean")
	}
}
