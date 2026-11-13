package p01

import "testing"

func TestMax(t *testing.T) {
	if Max([]int{3, 9, 2}) != 9 {
		t.Fatal(Max([]int{3, 9, 2}))
	}
}
