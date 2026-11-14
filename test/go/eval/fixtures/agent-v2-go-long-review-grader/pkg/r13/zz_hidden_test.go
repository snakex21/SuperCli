package r13

import "testing"

func TestHiddenReview(t *testing.T) {
	if JoinPath("a", "", "b", "") != "a/b" || JoinPath("", "x") != "x" || JoinPath("a", "b") != "a/b" {
		t.Fatal("JoinPath")
	}
}
