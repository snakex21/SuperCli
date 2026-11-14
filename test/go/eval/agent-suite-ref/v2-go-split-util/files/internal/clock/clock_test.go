package clock

import (
	"testing"
	"time"
)

func TestTime(t *testing.T) {
	if d, _ := ParseDurationLoose("2d"); d != 48*time.Hour {
		t.Fatal(d)
	}
	if got := FormatAgo(90 * time.Minute); got != "1h ago" {
		t.Fatal(got)
	}
}
