package r14

import "testing"

func TestHiddenReview(t *testing.T) {
	if it, ok := MaxBy([]Item{{"a", 1}, {"b", 3}, {"c", 3}}); !ok || it.Name != "b" {
		t.Fatal("MaxBy", it)
	}
}
