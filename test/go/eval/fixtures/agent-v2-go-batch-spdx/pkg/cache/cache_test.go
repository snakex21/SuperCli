package cache

import "testing"

func TestFillerWeights(t *testing.T) {
	if got := Weight1(Widget1{Count: 1}); got <= 0 {
		t.Fatalf("Weight1 = %d", got)
	}
	if got := Label1(Widget1{ID: "x"}); got == "" {
		t.Fatal("empty label")
	}
}
