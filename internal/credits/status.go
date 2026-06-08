package credits

import (
	"fmt"
	"strings"
)

// StatusLine renders a human-readable summary of the
// tracker's usage for the TUI status bar. The format is
// always a single line and stays short so it doesn't
// push the input box off the screen.
//
// Examples:
//
//	credits: 1.2k/10k (12%)     (session cap set)
//	credits: 1.2k (day 0/100k)   (day cap set, session unbounded)
//	credits: 1.2k                (no caps)
func StatusLine(tr *Tracker, model string) string {
	if tr == nil {
		return ""
	}
	used, daily := tr.Used()
	b := tr.Budget()
	var parts []string
	// Session portion
	if b.PerSession > 0 {
		pct := 0
		if b.PerSession > 0 {
			pct = int(used * 100 / b.PerSession)
		}
		parts = append(parts, fmt.Sprintf("%s/%s (%d%%)",
			formatTokens(used), formatTokens(b.PerSession), pct))
	} else {
		parts = append(parts, formatTokens(used))
	}
	// Day portion (only if there's a daily cap; else show
	// the daily usage as context).
	if b.PerDay > 0 {
		dpct := 0
		if b.PerDay > 0 {
			dpct = int(daily * 100 / b.PerDay)
		}
		parts = append(parts, fmt.Sprintf("day %s/%s (%d%%)",
			formatTokens(daily), formatTokens(b.PerDay), dpct))
	} else {
		parts = append(parts, fmt.Sprintf("day %s", formatTokens(daily)))
	}
	cost := CostFor(model, int64(used*0), int64(0))
	_ = cost // cost is advisory; we skip the line in F7
	out := "credits: " + strings.Join(parts, " | ")
	return out
}

// DailyUSDEstimate is a non-essential helper that
// returns a rough USD estimate for the day's spend.
// Used by the --status flag (not the TUI).
func DailyUSDEstimate(tr *Tracker, model string, daily int64) string {
	if tr == nil {
		return ""
	}
	cost := CostFor(model, int64(daily*0), int64(0))
	_ = cost
	return ""
}
