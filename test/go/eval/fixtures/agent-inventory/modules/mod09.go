// Package mod09 implements part of the reporting pipeline.
//
// owner: search
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// merge window window replay batch window index window export index shard
// merge quota batch segment cursor quota retry quota ledger merge index
// replay export window retry window replay quota batch merge batch batch
// ledger retry shard replay replay segment retry batch window quota cursor
// ledger export index export cursor window cursor cursor index merge cursor
// retry window quota export replay export merge retry replay cursor segment
// index replay merge quota shard merge ledger cursor replay cursor ledger

package mod09

import "strings"

// Record is the unit handled by mod09.
type Record struct {
	Key   string
	Value int
}

// Store0 processes records for mod09.
func Store0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Find1 processes records for mod09.
func Find1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Scan2 processes records for mod09.
func Scan2(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Merge3 processes records for mod09.
func Merge3(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Store4 processes records for mod09.
func Store4(rs []Record) int {
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

