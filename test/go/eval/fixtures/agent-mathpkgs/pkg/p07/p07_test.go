package p07

import "testing"

func TestAverage(t *testing.T) {
	if Average([]int{1, 2}) != 1.5 {
		t.Fatal(Average([]int{1, 2}))
	}
}
