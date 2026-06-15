package credits

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Budget is the set of caps a Tracker enforces. A zero
// value for either field means "no cap" — the field is
// ignored. Negative values are rejected by Validate.
type Budget struct {
	// PerSession caps the total tokens (input+output) for
	// the current session. 0 = no cap.
	PerSession int64
	// PerDay caps the total tokens across all sessions
	// whose credit_ledger rows fall in the current UTC
	// day. 0 = no cap.
	PerDay int64
}

// Validate returns nil if both caps are non-negative.
// Negative caps are a programming error and must be
// caught at config-load time, not silently clamped.
func (b Budget) Validate() error {
	if b.PerSession < 0 {
		return fmt.Errorf("credits: Budget.PerSession is negative: %d", b.PerSession)
	}
	if b.PerDay < 0 {
		return fmt.Errorf("credits: Budget.PerDay is negative: %d", b.PerDay)
	}
	return nil
}

// IsZero reports whether the budget has no caps at all.
// A zero Budget means the tracker will record every
// delta without ever returning ErrBudgetExceeded.
func (b Budget) IsZero() bool {
	return b.PerSession == 0 && b.PerDay == 0
}

// String returns a human-readable form suitable for the
// TUI status bar and the --status flag. "no caps" is
// returned when both fields are zero. The format is
// round-trip-safe through ParseBudget: each cap is
// comma-separated and uses the k/m compact form.
func (b Budget) String() string {
	if b.IsZero() {
		return "no caps"
	}
	var parts []string
	if b.PerSession > 0 {
		parts = append(parts, "session="+formatTokens(b.PerSession))
	}
	if b.PerDay > 0 {
		parts = append(parts, "day="+formatTokens(b.PerDay))
	}
	return strings.Join(parts, ",")
}

// ParseBudget decodes a "session=10k,day=100k" style
// string into a Budget. Empty string returns the zero
// budget (no caps). Unknown keys return an error.
func ParseBudget(s string) (Budget, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "no caps" || s == "0" {
		return Budget{}, nil
	}
	var b Budget
	for _, kv := range strings.Split(s, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			return Budget{}, fmt.Errorf("credits: ParseBudget: missing '=' in %q", kv)
		}
		key := strings.TrimSpace(kv[:eq])
		val := strings.TrimSpace(kv[eq+1:])
		n, err := parseTokens(val)
		if err != nil {
			return Budget{}, fmt.Errorf("credits: ParseBudget: %w", err)
		}
		switch key {
		case "session", "per_session", "perSession":
			b.PerSession = n
		case "day", "per_day", "perDay":
			b.PerDay = n
		default:
			return Budget{}, fmt.Errorf("credits: ParseBudget: unknown key %q (want session|day)", key)
		}
	}
	if err := b.Validate(); err != nil {
		return Budget{}, err
	}
	return b, nil
}

// parseTokens accepts plain integers ("1000"), thousands
// suffix ("10k", "10K"), and millions ("2m", "2M").
// Decimals ("1.5k") are rounded to nearest token.
func parseTokens(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty value")
	}
	mult := float64(1)
	last := s[len(s)-1]
	switch last {
	case 'k', 'K':
		mult = 1_000
		s = s[:len(s)-1]
	case 'm', 'M':
		mult = 1_000_000
		s = s[:len(s)-1]
	}
	if s == "" {
		return 0, fmt.Errorf("missing number in %q", s)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", s, err)
	}
	return int64(math.Round(f * mult)), nil
}

// formatTokens renders a token count in compact form.
// < 1000       -> "123"
// < 1_000_000  -> "1k" or "1.2k" (trailing zero elided)
// >= 1_000_000 -> "2m" or "2.3m"
func formatTokens(n int64) string {
	if n < 0 {
		return "-" + formatTokens(-n)
	}
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		// n / 100 = one decimal * 10
		// 1_500 / 100 = 15 -> "1.5k"
		// 1_000 / 100 = 10 -> "1k"
		// 999_999 / 100 = 9999 -> "999.9k"
		d := n / 100
		whole := d / 10
		tenth := d % 10
		if tenth == 0 {
			return strconv.FormatInt(whole, 10) + "k"
		}
		return strconv.FormatInt(whole, 10) + "." + strconv.FormatInt(tenth, 10) + "k"
	default:
		d := n / 100_000
		whole := d / 10
		tenth := d % 10
		if tenth == 0 {
			return strconv.FormatInt(whole, 10) + "m"
		}
		return strconv.FormatInt(whole, 10) + "." + strconv.FormatInt(tenth, 10) + "m"
	}
}
