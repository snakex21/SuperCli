package discount

import "testing"

func TestTiers(t *testing.T) {
	for qty, want := range map[int]int{1: 0, 10: 5, 49: 5, 50: 10, 150: 15} {
		if got := PercentFor(qty); got != want {
			t.Errorf("PercentFor(%d) = %d, want %d", qty, got, want)
		}
	}
	if got := Amount(1999, 5); got != 100 {
		t.Fatalf("Amount = %d", got)
	}
}
