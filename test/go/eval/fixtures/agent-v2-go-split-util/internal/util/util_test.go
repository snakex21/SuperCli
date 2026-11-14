package util

import (
	"strings"
	"testing"
	"time"
)

func TestStrings(t *testing.T) {
	if got := Slugify("Hello, World 2"); got != "hello-world-2" {
		t.Fatal(got)
	}
	if got := Truncate("abcdefgh", 6); got != "abc..." {
		t.Fatal(got)
	}
}

func TestTime(t *testing.T) {
	if d, _ := ParseDurationLoose("2d"); d != 48*time.Hour {
		t.Fatal(d)
	}
	if got := FormatAgo(90 * time.Minute); got != "1h ago" {
		t.Fatal(got)
	}
}

func TestIDs(t *testing.T) {
	if id := NewID("req"); !strings.HasPrefix(id, "req_") {
		t.Fatal(id)
	}
}
