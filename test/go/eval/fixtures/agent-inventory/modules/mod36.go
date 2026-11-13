// Package mod36 implements part of the reporting pipeline.
//
// owner: search
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// cursor retry ledger shard merge export batch segment cursor segment cursor
// quota ledger merge window ledger export quota cursor shard shard retry
// export quota ledger replay export window ledger batch segment quota ledger
// batch cursor retry window segment merge quota merge retry cursor batch
// batch retry batch window replay shard merge index replay merge retry
// merge segment quota quota merge quota ledger batch replay ledger batch
// quota ledger batch window cursor window batch shard replay cursor batch
// export retry segment merge merge ledger window cursor merge export window

package mod36

import "strings"

// Record is the unit handled by mod36.
type Record struct {
	Key   string
	Value int
}

// Flush0 processes records for mod36.
func Flush0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Build1 processes records for mod36.
func Build1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Merge2 processes records for mod36.
func Merge2(rs []Record) int {
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

// Normalize0 is a method, not a top-level function.
func (r *Record) Normalize0() {
	r.Key = helper0(r.Key)
}

// Normalize1 is a method, not a top-level function.
func (r *Record) Normalize1() {
	r.Key = helper0(r.Key)
}

