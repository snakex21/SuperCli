package p12

import "testing"

func TestMedian(t *testing.T) {
	if Median([]float64{4, 1, 3, 2}) != 2.5 || Median([]float64{3, 1, 2}) != 2 {
		t.Fatal("median")
	}
}
