package r09

import "testing"

func TestVisible(t *testing.T) {
	if Truncate("ab", 5) != "ab" {
		t.Fatal("trunc")
	}
}
