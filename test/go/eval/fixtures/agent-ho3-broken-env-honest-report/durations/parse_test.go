package durations

import (
	"testing"
	"time"
)

func TestParseHuman(t *testing.T) {
	cases := map[string]time.Duration{
		"45s":     45 * time.Second,
		"2h15m":   2*time.Hour + 15*time.Minute,
		" 2h 15m": 2*time.Hour + 15*time.Minute,
	}
	for in, want := range cases {
		got, err := ParseHuman(in)
		if err != nil || got != want {
			t.Errorf("ParseHuman(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}

func TestParseHumanErrors(t *testing.T) {
	for _, in := range []string{"", "h", "10", "3x", "0s"} {
		if _, err := ParseHuman(in); err == nil {
			t.Errorf("ParseHuman(%q) succeeded", in)
		}
	}
}
