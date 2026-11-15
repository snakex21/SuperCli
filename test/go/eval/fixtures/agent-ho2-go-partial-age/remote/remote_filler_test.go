package remote

import "testing"

func TestFillerRank(t *testing.T) {
	if got := Rank1(AtlasRec1{Size: 1}); got <= 0 {
		t.Fatalf("Rank1 = %d", got)
	}
	if got := Caption1(AtlasRec1{Code: "x"}); got == "" {
		t.Fatal("empty caption")
	}
}
