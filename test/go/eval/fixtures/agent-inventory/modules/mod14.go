// Package mod14 implements part of the reporting pipeline.
//
// owner: growth
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// batch export quota export export shard retry cursor quota shard export
// retry replay batch ledger cursor cursor window merge shard export merge
// window export quota cursor retry batch quota ledger ledger ledger cursor
// export ledger merge shard retry index quota index ledger export cursor
// retry segment batch ledger ledger retry index window merge index export
// ledger index ledger cursor quota merge retry shard window ledger ledger
// retry cursor window merge merge ledger quota export window ledger batch

package mod14

import "strings"

// Record is the unit handled by mod14.
type Record struct {
	Key   string
	Value int
}

// Build0 processes records for mod14.
func Build0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Apply1 processes records for mod14.
func Apply1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Store2 processes records for mod14.
func Store2(rs []Record) int {
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

// Normalize0 is a method, not a top-level function.
func (r *Record) Normalize0() {
	r.Key = helper0(r.Key)
}

// Normalize1 is a method, not a top-level function.
func (r *Record) Normalize1() {
	r.Key = helper0(r.Key)
}

