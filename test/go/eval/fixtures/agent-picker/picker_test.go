package picker

import "testing"

// Product spec v1: Pick returns the first element.
func TestPickFirst(t *testing.T) {
	if got := Pick([]int{4, 9}); got != 4 {
		t.Fatalf("Pick = %d, want 4", got)
	}
}

// Product spec v2: Pick returns the largest element.
func TestPickLargest(t *testing.T) {
	if got := Pick([]int{4, 9}); got != 9 {
		t.Fatalf("Pick = %d, want 9", got)
	}
}
