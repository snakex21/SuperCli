// Package mod02 implements part of the reporting pipeline.
//
// owner: search
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// merge index merge replay quota segment quota export index segment quota
// index quota replay merge export export cursor segment segment batch batch
// window index shard merge quota merge retry cursor segment replay merge
// merge quota merge export quota index batch segment retry merge ledger
// merge replay replay quota segment merge window export replay merge ledger
// index segment merge retry ledger cursor replay export window merge batch
// ledger batch merge shard quota ledger shard cursor cursor ledger shard
// batch quota segment index index retry window batch retry index shard
// shard merge replay replay window quota cursor merge shard window shard
// replay quota segment ledger export index window window shard segment quota
// export export window quota cursor export batch ledger export ledger batch

package mod02

import "strings"

// Record is the unit handled by mod02.
type Record struct {
	Key   string
	Value int
}

// Find0 processes records for mod02.
func Find0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Find1 processes records for mod02.
func Find1(rs []Record) int {
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

