package textutil

import "testing"

func TestHiddenTextutil(t *testing.T) {
	if Slugify("  Go -- Rocks!! ") != "go-rocks" || Truncate("żółwik", 4) != "ż..." {
		t.Fatal("textutil behaviour changed")
	}
}
