package mathx

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatal("bad")
	}
}

func TestAddNegative(t *testing.T) {
	if Add(-2, 3) != 1 {
		t.Fatal("bad")
	}
}
