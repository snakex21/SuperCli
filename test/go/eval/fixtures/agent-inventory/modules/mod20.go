// Package mod20 implements part of the reporting pipeline.
//
// owner: growth
// status: experimental
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// replay ledger export replay export ledger ledger segment export cursor retry
// ledger ledger batch window batch cursor quota cursor batch batch segment
// batch index cursor batch batch segment window export cursor merge ledger
// export merge ledger retry retry window shard cursor batch ledger window
// export merge batch quota replay ledger window shard merge merge batch
// retry merge shard replay window shard replay shard window cursor shard
// quota ledger replay cursor cursor merge batch window merge index ledger
// quota export ledger retry quota ledger window shard shard shard ledger
// segment merge batch index export merge retry retry segment quota retry
// batch export export cursor retry ledger export merge export quota window

package mod20

import "strings"

// Record is the unit handled by mod20.
type Record struct {
	Key   string
	Value int
}

// Find0 processes records for mod20.
func Find0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Scan1 processes records for mod20.
func Scan1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Build2 processes records for mod20.
func Build2(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Split3 processes records for mod20.
func Split3(rs []Record) int {
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

