package r02

import "testing"

func TestVisible(t *testing.T) {
	if Slug("Go Lang") != "go-lang" {
		t.Fatal("slug")
	}
}
