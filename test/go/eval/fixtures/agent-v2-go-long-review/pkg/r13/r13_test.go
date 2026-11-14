package r13

import "testing"

func TestVisible(t *testing.T) {
	if JoinPath("a", "b") != "a/b" {
		t.Fatal("join")
	}
}
