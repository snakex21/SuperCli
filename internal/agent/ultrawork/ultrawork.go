// Package ultrawork implements the F9 "ultrawork" mode of the
// SuperCli agent. When the user prompt contains the keyword
// "ultrawork" (or the abbreviation "ulw" as a standalone word),
// the agent loop operates in a full-autonomy mode that:
//
//   - Injects an extra system-prompt section reminding the model
//     to decompose work into parallel sub-agents, use the
//     background-task pattern for long-running commands, and not
//     declare done until the active /goal's todos are all checked
//     off.
//   - Re-prompts the model (the "Sisyphus" enforcer) if it stops
//     with unfinished tasks still on the active /goal.
//
// Ultrawork is gated on two things:
//
//   1. An active /goal (F8). Ultrawork without a target is just
//      unbounded agent autonomy, which the user almost never
//      wants.
//   2. A positive credit balance (F7). Ultrawork runs are
//      token-hungry; we refuse to enter the mode if the session
//      has no budget left.
//
// The package depends on neither goal nor credits directly;
// it defines small GoalGate / CreditGate interfaces that the
// main wiring adapts. This keeps the agent loop free of
// concrete knowledge about the F8 / F7 packages and lets the
// F9 unit tests run with simple mock gates.
package ultrawork

import (
	"strings"
)

// Mode is the per-Run ultrawork switch. Stored on the Loop at
// the start of Run and consulted in run() before the
// Sisyphus enforcer kicks in.
type Mode int

const (
	// ModeOff is the default. No ultrawork behavior.
	ModeOff Mode = iota
	// ModeOn means the keyword was detected, the gates passed,
	// and the system-prompt section + Sisyphus are active.
	ModeOn
)

// String returns a stable lowercase identifier for log lines
// and the --status flag.
func (m Mode) String() string {
	switch m {
	case ModeOn:
		return "on"
	default:
		return "off"
	}
}

// Wiring bundles the F9 dependencies the agent loop needs
// in a single LoopConfig field. main.go constructs one of
// these from the live *goal.Service and *credits.Tracker.
//
// A nil Wiring means "F9 disabled" — the loop ignores the
// keyword, never injects the system section, never runs
// Sisyphus. This is the default; ultrawork is opt-in.
//
// Note: Wiring intentionally holds INTERFACES, not pointers
// to concrete goal/credits packages, so the ultrawork
// package (and the agent package that depends on it) stay
// free of those imports.
type Wiring struct {
	// Goal is the active /goal gate. Required when Wiring
	// is non-nil; the loop's gate check fails fast if it
	// is nil.
	Goal GoalGate
	// Credit is the credit-budget gate. Optional — when
	// nil, the loop treats the credit gate as "no cap".
	Credit CreditGate
	// SisyphusMax caps the number of consecutive Sisyphus
	// re-prompts in a single Run. Zero means default (3).
	// See Sisyphus.MaxConsecutive for the full semantics.
	SisyphusMax int
}

// Detect returns true when the user prompt contains the
// "ultrawork" keyword (case-insensitive substring) or the
// abbreviation "ulw" as a standalone word (so we don't false-
// positive on random occurrences of the letters u-l-w inside
// a longer identifier). An empty prompt is always false.
//
// "standalone word" means: preceded by start-of-string or a
// non-letter, and followed by end-of-string or a non-letter.
// The model can also append punctuation (".ulw.", ", ulw!",
// "(ulw)") and still trigger.
func Detect(prompt string) bool {
	if prompt == "" {
		return false
	}
	lower := strings.ToLower(prompt)
	if strings.Contains(lower, "ultrawork") {
		return true
	}
	return containsWord(lower, "ulw")
}

// containsWord reports whether needle appears in haystack
// surrounded by non-letter runes (or string boundaries).
// The caller must pre-lowercase both.
func containsWord(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); {
		j := strings.Index(haystack[i:], needle)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(needle)
		leftOK := start == 0 || !isLetter(haystack[start-1])
		rightOK := end == len(haystack) || !isLetter(haystack[end])
		if leftOK && rightOK {
			return true
		}
		i = start + 1
	}
	return false
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
