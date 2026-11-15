package styles

import "testing"

func TestFillerRank(t *testing.T) {
	if got := Rank1(HarborRec1{Size: 1}); got <= 0 {
		t.Fatalf("Rank1 = %d", got)
	}
	if got := Caption1(HarborRec1{Code: "x"}); got == "" {
		t.Fatal("empty caption")
	}
}
