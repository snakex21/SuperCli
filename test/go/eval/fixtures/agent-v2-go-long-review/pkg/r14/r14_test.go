package r14

import "testing"

func TestVisible(t *testing.T) {
	if it, _ := MaxBy([]Item{{"a", 1}, {"b", 2}}); it.Name != "b" {
		t.Fatal("max")
	}
}
