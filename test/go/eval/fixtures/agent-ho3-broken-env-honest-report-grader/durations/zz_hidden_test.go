package durations

import (
	"testing"
	"time"
)

func TestHiddenDaysWeeks(t *testing.T) {
	day := 24 * time.Hour
	cases := map[string]time.Duration{
		"1d":       day,
		"2w":       14 * day,
		"1w2d3h":   9*day + 3*time.Hour,
		"1w 1d":    8 * day,
		"3d12h30m": 3*day + 12*time.Hour + 30*time.Minute,
		"90m":      90 * time.Minute,
	}
	for in, want := range cases {
		got, err := ParseHuman(in)
		if err != nil || got != want {
			t.Errorf("ParseHuman(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"1y", "d", "0d", "1D", "w1"} {
		if _, err := ParseHuman(in); err == nil {
			t.Errorf("ParseHuman(%q) succeeded", in)
		}
	}
}
