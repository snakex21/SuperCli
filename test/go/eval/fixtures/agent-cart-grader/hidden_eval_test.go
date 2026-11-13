package cart

import "testing"

func TestHiddenCartEdges(t *testing.T) {
	if Subtotal(nil) != 0 {
		t.Fatal("empty cart")
	}
	if got := Subtotal([]Item{{"x", 199, 3}}); got != 597 {
		t.Fatalf("single item = %d", got)
	}
	if got := Total([]Item{{"x", 10000, 1}}); got != 9000 {
		t.Fatalf("boundary total = %d", got)
	}
}
