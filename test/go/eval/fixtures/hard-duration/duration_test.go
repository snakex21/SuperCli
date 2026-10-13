package humandur

import (
	"testing"
	"time"
)

func TestParseBasic(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"90s", 90 * time.Second},
		{"1h30m", 90 * time.Minute},
		{"2d", 48 * time.Hour},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Fatalf("Parse(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestFormatBasic(t *testing.T) {
	if got := Format(90 * time.Minute); got != "1h30m" {
		t.Errorf("Format(90m) = %q, want %q", got, "1h30m")
	}
}
