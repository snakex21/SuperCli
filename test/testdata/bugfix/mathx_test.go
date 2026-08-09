package mathx

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
}

func TestAddNegative(t *testing.T) {
	if got := Add(-1, -2); got != -3 {
		t.Fatalf("Add(-1, -2) = %d, want -3", got)
	}
}
