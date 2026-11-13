// Package mod28 implements part of the reporting pipeline.
//
// owner: infra
// status: experimental
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// ledger cursor index index segment batch export shard index retry export
// cursor ledger segment cursor shard index retry segment replay export ledger
// quota batch replay merge retry export merge quota ledger merge window
// replay segment replay export export segment ledger window window batch window
// quota segment quota retry window index retry quota batch window index
// retry index merge shard index merge quota ledger merge export quota
// quota merge quota segment shard shard shard export index ledger merge
// shard merge quota index retry retry merge retry export export batch
// replay retry retry segment index quota cursor replay index replay index

package mod28

import "strings"

// Record is the unit handled by mod28.
type Record struct {
	Key   string
	Value int
}

// Apply0 processes records for mod28.
func Apply0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Build1 processes records for mod28.
func Build1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Load2 processes records for mod28.
func Load2(rs []Record) int {
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

