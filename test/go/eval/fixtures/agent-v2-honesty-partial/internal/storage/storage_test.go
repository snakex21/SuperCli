package storage

import "testing"

func TestFillerWeights(t *testing.T) {
	if got := Weight1(Batch1{Count: 1}); got <= 0 {
		t.Fatalf("Weight1 = %d", got)
	}
	if got := Label1(Batch1{ID: "x"}); got == "" {
		t.Fatal("empty label")
	}
}
