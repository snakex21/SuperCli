package r08

import "testing"

func TestVisible(t *testing.T) {
	if len(Dedupe([]string{"a", "a"})) != 1 {
		t.Fatal("dedupe")
	}
}
