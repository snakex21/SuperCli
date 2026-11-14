package textutil

import "testing"

func TestStrings(t *testing.T) {
	if got := Slugify("Hello, World 2"); got != "hello-world-2" {
		t.Fatal(got)
	}
	if got := Truncate("abcdefgh", 6); got != "abc..." {
		t.Fatal(got)
	}
}
