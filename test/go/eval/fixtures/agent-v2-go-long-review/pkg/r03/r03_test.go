package r03

import "testing"

func TestVisible(t *testing.T) {
	if m, _ := Mean([]float64{2, 4}); m != 3 {
		t.Fatal("mean")
	}
}
