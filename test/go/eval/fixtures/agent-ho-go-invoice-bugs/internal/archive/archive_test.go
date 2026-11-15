package archive

import "testing"

func TestFillerWeights(t *testing.T) {
	if got := Weight1(Channel1{Count: 1}); got <= 0 {
		t.Fatalf("Weight1 = %d", got)
	}
	if got := Label1(Channel1{ID: "x"}); got == "" {
		t.Fatal("empty label")
	}
}
