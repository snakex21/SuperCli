package r12

import "testing"

func TestVisible(t *testing.T) {
	if len(Chunk([]int{1, 2, 3}, 1)) != 3 {
		t.Fatal("chunk")
	}
}
