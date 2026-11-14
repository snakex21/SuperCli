package r06

import "testing"

func TestVisible(t *testing.T) {
	if len(SplitFields("a,b")) != 2 {
		t.Fatal("split")
	}
}
