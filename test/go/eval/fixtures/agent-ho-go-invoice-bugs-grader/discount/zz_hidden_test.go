package discount

import "testing"

func TestHiddenBoundary(t *testing.T) {
	for qty, want := range map[int]int{9: 0, 99: 10, 100: 15, 101: 15} {
		if got := PercentFor(qty); got != want {
			t.Errorf("PercentFor(%d) = %d, want %d", qty, got, want)
		}
	}
}
