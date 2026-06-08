package credits

import (
	"strings"
	"testing"
)

func TestBudget_Validate(t *testing.T) {
	cases := []struct {
		name    string
		b       Budget
		wantErr bool
	}{
		{"zero budget", Budget{}, false},
		{"positive session", Budget{PerSession: 1000}, false},
		{"positive day", Budget{PerDay: 5000}, false},
		{"both positive", Budget{PerSession: 1000, PerDay: 5000}, false},
		{"negative session", Budget{PerSession: -1}, true},
		{"negative day", Budget{PerDay: -1}, true},
		{"both negative", Budget{PerSession: -1, PerDay: -1}, true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			err := c.b.Validate()
			if (err != nil) != c.wantErr {
				t.Errorf("Validate() err=%v, wantErr=%v", err, c.wantErr)
			}
		})
	}
}

func TestBudget_IsZero(t *testing.T) {
	if !(Budget{}).IsZero() {
		t.Error("zero budget should be zero")
	}
	if (Budget{PerSession: 1}).IsZero() {
		t.Error("non-zero session cap should make IsZero false")
	}
	if (Budget{PerDay: 1}).IsZero() {
		t.Error("non-zero day cap should make IsZero false")
	}
}

func TestBudget_String(t *testing.T) {
	cases := []struct {
		b    Budget
		want string
	}{
		{Budget{}, "no caps"},
		{Budget{PerSession: 1000}, "session=1k"},
		{Budget{PerDay: 5_000_000}, "day=5m"},
		{Budget{PerSession: 1500, PerDay: 1_200_000}, "session=1.5k,day=1.2m"},
	}
	for _, c := range cases {
		if got := c.b.String(); got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
	}
}

func TestParseBudget(t *testing.T) {
	cases := []struct {
		in   string
		want Budget
		err  bool
	}{
		{"", Budget{}, false},
		{"no caps", Budget{}, false},
		{"0", Budget{}, false},
		{"session=1k", Budget{PerSession: 1000}, false},
		{"session=10k,day=100k", Budget{PerSession: 10_000, PerDay: 100_000}, false},
		{"per_session=5k,per_day=50k", Budget{PerSession: 5000, PerDay: 50_000}, false},
		{"  session = 2.5k , day = 1m ", Budget{PerSession: 2500, PerDay: 1_000_000}, false},
		{"session=-1", Budget{}, true},
		{"foo=1k", Budget{}, true},
		{"session=abc", Budget{}, true},
		{"session", Budget{}, true},
		{"session=", Budget{}, true},
	}
	for _, c := range cases {
		got, err := ParseBudget(c.in)
		if (err != nil) != c.err {
			t.Errorf("ParseBudget(%q) err=%v, wantErr=%v", c.in, err, c.err)
			continue
		}
		if !c.err && got != c.want {
			t.Errorf("ParseBudget(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestFormatTokens(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{999, "999"},
		{1000, "1k"},
		{1234, "1.2k"},
		{15_000, "15k"},
		{15_500, "15.5k"},
		{999_999, "999.9k"},
		{1_000_000, "1m"},
		{2_300_000, "2.3m"},
		{-1500, "-1.5k"},
	}
	for _, c := range cases {
		if got := formatTokens(c.n); got != c.want {
			t.Errorf("formatTokens(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestParseTokens(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		err  bool
	}{
		{"1000", 1000, false},
		{"1k", 1000, false},
		{"10K", 10_000, false},
		{"2m", 2_000_000, false},
		{"5M", 5_000_000, false},
		{"1.5k", 1500, false},
		{"", 0, true},
		{"abc", 0, true},
		{"k", 0, true},
	}
	for _, c := range cases {
		got, err := parseTokens(c.in)
		if (err != nil) != c.err {
			t.Errorf("parseTokens(%q) err=%v, wantErr=%v", c.in, err, c.err)
			continue
		}
		if !c.err && got != c.want {
			t.Errorf("parseTokens(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestBudget_RoundTrip(t *testing.T) {
	// Compact form is lossy; use values that round-trip
	// exactly (whole thousands, no fractional k/m).
	original := Budget{PerSession: 12_000, PerDay: 987_000}
	parsed, err := ParseBudget(original.String())
	if err != nil {
		t.Fatalf("ParseBudget(%q) failed: %v", original.String(), err)
	}
	if parsed != original {
		t.Errorf("round trip lost data: %+v != %+v", parsed, original)
	}
}

func TestSource_Valid(t *testing.T) {
	cases := []struct {
		s    Source
		want bool
	}{
		{"", true},
		{SourceLoop, true},
		{SourceSubAgent, true},
		{SourceDarwin, true},
		{SourceJudge, true},
		{SourceReflector, true},
		{"unknown", false},
		{"Loop", false}, // case-sensitive
	}
	for _, c := range cases {
		if got := c.s.Valid(); got != c.want {
			t.Errorf("Source(%q).Valid() = %v, want %v", c.s, got, c.want)
		}
	}
}

// Smoke: cover unused import (strings) by ensuring the
// package file is real; nothing in the API uses strings
// directly but future helpers will.
var _ = strings.TrimSpace
