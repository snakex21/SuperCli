// Package mod32 implements part of the reporting pipeline.
//
// owner: search
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// retry merge retry replay cursor merge batch replay replay merge cursor
// batch quota batch shard batch quota export window merge merge cursor
// window export segment segment retry window merge segment cursor export index
// quota batch segment cursor ledger batch segment replay replay ledger merge
// window export ledger segment index index window window retry ledger retry
// window ledger export index retry merge export merge replay retry index
// window export batch retry quota index replay ledger quota quota replay

package mod32

import "strings"

// Record is the unit handled by mod32.
type Record struct {
	Key   string
	Value int
}

// Find0 processes records for mod32.
func Find0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Flush1 processes records for mod32.
func Flush1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Scan2 processes records for mod32.
func Scan2(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Split3 processes records for mod32.
func Split3(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Flush4 processes records for mod32.
func Flush4(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

func helper0(s string) string {
	return strings.TrimSpace(s)
}

func helper1(s string) string {
	return strings.TrimSpace(s)
}

func helper2(s string) string {
	return strings.TrimSpace(s)
}

// Normalize0 is a method, not a top-level function.
func (r *Record) Normalize0() {
	r.Key = helper0(r.Key)
}

// Normalize1 is a method, not a top-level function.
func (r *Record) Normalize1() {
	r.Key = helper0(r.Key)
}

