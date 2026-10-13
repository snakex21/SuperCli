//go:build evalhidden

package humandur

import (
	"errors"
	"math"
	"testing"
	"time"
)

const day = 24 * time.Hour

func TestHiddenParse(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"0s", 0},
		{"0d", 0},
		{"1ms", time.Millisecond},
		{"1m", time.Minute},
		{"1m1ms", time.Minute + time.Millisecond},
		{"1d2h3m4s5ms", day + 2*time.Hour + 3*time.Minute + 4*time.Second + 5*time.Millisecond},
		{"36h", 36 * time.Hour},
		{"90m", 90 * time.Minute},
		{"1500ms", 1500 * time.Millisecond},
		{"007s", 7 * time.Second},
		{"1h 30m", 90 * time.Minute},
		{"  2d   4h  ", 2*day + 4*time.Hour},
		{"1d30s", day + 30*time.Second},
		{"106751d23h47m16s854ms", 106751*day + 23*time.Hour + 47*time.Minute + 16*time.Second + 854*time.Millisecond},
		{"9223372036854ms", 9223372036854 * time.Millisecond},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestHiddenParseSyntaxErrors(t *testing.T) {
	bad := []string{
		"", "   ", "5", "1h30", "h", "1x", "1H", "1.5h", "-1h", "+1h", "1 h",
		"30m1h", "1h1h", "1s1m", "1ms1s", "1m1m", "1d1d", "1h,30m", "1µs", "1us", "1ns",
		"1hh", "1mss", "d1", "1h-30m", "1ｈ",
	}
	for _, s := range bad {
		got, err := Parse(s)
		if !errors.Is(err, ErrSyntax) || got != 0 {
			t.Errorf("Parse(%q) = %v, %v; want 0, ErrSyntax", s, got, err)
		}
	}
}

func TestHiddenParseRangeErrors(t *testing.T) {
	bad := []string{
		"106752d",
		"106751d23h47m16s855ms",
		"9223372036855ms",
		"99999999999999999999s",
		"999999999999999999999999999999ms",
		"100000d100000d",
		"106751d24h",
	}
	for _, s := range bad {
		got, err := Parse(s)
		if s == "100000d100000d" {
			if !errors.Is(err, ErrSyntax) && !errors.Is(err, ErrRange) {
				t.Errorf("Parse(%q) = %v, %v; want an error", s, got, err)
			}
			continue
		}
		if !errors.Is(err, ErrRange) || got != 0 {
			t.Errorf("Parse(%q) = %v, %v; want 0, ErrRange", s, got, err)
		}
	}
}

func TestHiddenFormat(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{999 * time.Microsecond, "0s"},
		{-999 * time.Microsecond, "0s"},
		{time.Millisecond, "1ms"},
		{1500 * time.Millisecond, "1s500ms"},
		{1999999 * time.Microsecond, "1s999ms"},
		{26*time.Hour + 3*time.Minute, "1d2h3m"},
		{day, "1d"},
		{day + time.Millisecond, "1d1ms"},
		{-90 * time.Minute, "-1h30m"},
		{-1500 * time.Microsecond, "-1ms"},
		{time.Duration(math.MaxInt64), "106751d23h47m16s854ms"},
		{time.Duration(math.MinInt64), "-106751d23h47m16s854ms"},
	}
	for _, c := range cases {
		if got := Format(c.in); got != c.want {
			t.Errorf("Format(%d) = %q, want %q", int64(c.in), got, c.want)
		}
	}
}

func TestHiddenRoundTrip(t *testing.T) {
	values := []time.Duration{
		0, time.Millisecond, 59 * time.Second, 61 * time.Minute, 3*day + 7*time.Millisecond,
		1234567890123 * time.Microsecond, time.Duration(math.MaxInt64),
	}
	for _, d := range values {
		s := Format(d)
		got, err := Parse(s)
		want := d.Truncate(time.Millisecond)
		if err != nil || got != want {
			t.Errorf("Parse(Format(%d)) = Parse(%q) = %v, %v; want %v", int64(d), s, got, err, want)
		}
	}
}
