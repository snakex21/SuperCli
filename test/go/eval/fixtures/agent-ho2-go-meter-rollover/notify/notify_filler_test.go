package notify

import "testing"

func TestFillerRank(t *testing.T) {
	if got := Rank1(RelayRec1{Size: 1}); got <= 0 {
		t.Fatalf("Rank1 = %d", got)
	}
	if got := Caption1(RelayRec1{Code: "x"}); got == "" {
		t.Fatal("empty caption")
	}
}
