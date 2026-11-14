package r02

import "testing"

func TestHiddenReview(t *testing.T) {
	if Slug("  A  b ") != "a-b" || Slug("-x--y-") != "x-y" || Slug("Go Lang") != "go-lang" {
		t.Fatal("Slug")
	}
}
