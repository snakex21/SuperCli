package routing

import "testing"

func TestFillerWeights(t *testing.T) {
	if got := Weight1(Session1{Count: 1}); got <= 0 {
		t.Fatalf("Weight1 = %d", got)
	}
	if got := Label1(Session1{ID: "x"}); got == "" {
		t.Fatal("empty label")
	}
}
