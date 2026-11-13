// Package mod03 implements part of the reporting pipeline.
//
// owner: payments
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// merge retry ledger merge merge shard shard window window shard quota
// cursor export shard retry merge shard cursor index replay ledger quota
// export batch cursor index window batch index quota batch merge index
// cursor merge replay index batch window index quota cursor quota window
// window index window export segment export quota export ledger export segment
// window retry window segment merge merge export segment merge window retry
// quota merge merge ledger quota quota segment ledger cursor cursor batch
// ledger merge index retry export quota index window batch retry window

package mod03

import "strings"

// Record is the unit handled by mod03.
type Record struct {
	Key   string
	Value int
}

// Find0 processes records for mod03.
func Find0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Flush1 processes records for mod03.
func Flush1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Scan2 processes records for mod03.
func Scan2(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Flush3 processes records for mod03.
func Flush3(rs []Record) int {
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

