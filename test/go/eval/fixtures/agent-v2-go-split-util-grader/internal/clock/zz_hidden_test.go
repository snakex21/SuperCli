package clock

import (
	"testing"
	"time"
)

func TestHiddenClock(t *testing.T) {
	if d, err := ParseDurationLoose("1h30m"); err != nil || d != 90*time.Minute {
		t.Fatal(d, err)
	}
	if FormatAgo(49*time.Hour) != "2d ago" {
		t.Fatal("FormatAgo")
	}
}
