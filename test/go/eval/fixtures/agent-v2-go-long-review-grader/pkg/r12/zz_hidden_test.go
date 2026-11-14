package r12

import "testing"

func TestHiddenReview(t *testing.T) {
	if Chunk([]int{1, 2}, 0) != nil || Chunk([]int{1}, -3) != nil || len(Chunk([]int{1, 2, 3}, 2)) != 2 {
		t.Fatal("Chunk")
	}
}
