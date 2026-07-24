// Package stats records per-turn metrics for a session and
// renders them via the --stats command. F2.g covers the basic
// recorder + printer; the on-disk format is a small JSON file
// inside the home directory so it can be inspected by hand.
package stats

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

func Print(w io.Writer, turns []Turn) {
	if len(turns) == 0 {
		fmt.Fprintln(w, "no stats recorded")
		return
	}
	fmt.Fprintf(w, "%-4s %-8s %-8s %-9s %-6s %-8s %s\n", "step", "in", "out", "ms", "calls", "saved", "tools")
	for _, t := range turns {
		fmt.Fprintf(w, "%-4d %-8d %-8d %-9d %-6d %-8d %s\n",
			t.Step, t.TokensIn, t.TokensOut, t.DurationMs, t.ToolCalls, t.TokensSaved, joinTools(t.Tools))
		if p := FormatPhases(t.Phases); p != "" {
			fmt.Fprintf(w, "     %s\n", p)
		}
	}
	total := Sum(turns)
	fmt.Fprintf(w, "\ntotal: %d turns, %d in, %d out, %d combined, %d saved by drafts\n",
		total.Turns, total.TokensIn, total.TokensOut, total.TokensIn+total.TokensOut, total.TokensSaved)
	fmt.Fprintf(w, "tool calls: %d total, %.1f avg/step, %d step(s) with >1 call\n",
		total.ToolCalls, float64(total.ToolCalls)/float64(total.Turns), total.MultiCall)
	if p := FormatPhases(SumPhases(turns)); p != "" {
		fmt.Fprintf(w, "phase totals: %s\n", p)
	}
}

// FormatCallAgg renders one per-purpose aggregate as a compact
// single line: purpose, call count, total wall time, average TTFT,
// tokens, plus background/canceled/failed markers when non-zero.
func FormatCallAgg(a CallAgg) string {
	s := fmt.Sprintf("%s: %d call(s) %s", a.Purpose, a.Count, formatUs(a.TotalUs))
	if a.TTFTCount > 0 {
		s += fmt.Sprintf(" ttft~%s", formatUs(a.TTFTUs/int64(a.TTFTCount)))
	}
	s += fmt.Sprintf(" in=%d out=%d", a.TokensIn, a.TokensOut)
	if a.Background > 0 {
		s += fmt.Sprintf(" bg=%d", a.Background)
	}
	if a.Canceled > 0 {
		s += fmt.Sprintf(" canceled=%d", a.Canceled)
	}
	if a.Failed > 0 {
		s += fmt.Sprintf(" failed=%d", a.Failed)
	}
	return s
}

// CallsLine renders the per-purpose aggregate of calls as one
// machine-greppable stderr line for batch mode, mirroring
// PhaseLine. Returns "" when there are no calls.
func CallsLine(calls []Call) string {
	if len(calls) == 0 {
		return ""
	}
	parts := make([]string, 0, 4)
	for _, a := range SumCalls(calls) {
		parts = append(parts, fmt.Sprintf("%s=%dx/%s/in=%d/out=%d",
			a.Purpose, a.Count, formatUs(a.TotalUs), a.TokensIn, a.TokensOut))
	}
	return "[calls] " + strings.Join(parts, " ")
}

// FormatPhases renders a phase map (µs) as one compact line:
// canonical phases first in pipeline order, then any extra keys
// (per-tool entries) sorted. Zero-valued canonical phases are
// skipped. Returns "" when there is nothing to show.
func FormatPhases(phases map[string]int64) string {
	if len(phases) == 0 {
		return ""
	}
	parts := make([]string, 0, len(phases))
	seen := make(map[string]struct{}, len(phases))
	for _, k := range phaseOrder {
		seen[k] = struct{}{}
		if v, ok := phases[k]; ok {
			parts = append(parts, fmt.Sprintf("%s=%s", k, formatUs(v)))
		}
	}
	extra := make([]string, 0, len(phases))
	for k := range phases {
		if _, ok := seen[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		parts = append(parts, fmt.Sprintf("%s=%s", k, formatUs(phases[k])))
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

// formatUs renders microseconds human-readably: sub-millisecond
// values keep µs precision (the cheap phases must not round to
// zero — proving them cheap is the point), everything else is ms.
func formatUs(us int64) string {
	switch {
	case us < 1000:
		return fmt.Sprintf("%dµs", us)
	case us < 10_000:
		return fmt.Sprintf("%.1fms", float64(us)/1000)
	default:
		return fmt.Sprintf("%dms", us/1000)
	}
}

// PhaseLine renders one turn as a single machine-greppable line
// for batch mode stderr: [phase] step=N calls=N in=N out=N
// followed by the phase breakdown. Returns "" for a turn with
// no phase data so callers can skip silently.
func PhaseLine(t Turn) string {
	p := FormatPhases(t.Phases)
	if p == "" {
		return ""
	}
	return fmt.Sprintf("[phase] step=%d calls=%d in=%d out=%d %s",
		t.Step, t.ToolCalls, t.TokensIn, t.TokensOut, p)
}

func joinTools(tools []string) string {
	if len(tools) == 0 {
		return "-"
	}
	out := ""
	for i, n := range tools {
		if i > 0 {
			out += ","
		}
		out += n
	}
	return out
}
