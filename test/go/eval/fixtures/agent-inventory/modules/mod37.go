// Package mod37 implements part of the reporting pipeline.
//
// owner: payments
// status: deprecated
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// export ledger quota quota ledger segment segment cursor cursor merge export
// batch batch ledger window ledger window index batch replay export export
// export ledger window batch index retry retry shard index cursor replay
// window quota window retry batch merge index replay batch ledger segment
// export retry merge export ledger window export retry replay window segment
// shard ledger cursor index index quota ledger ledger segment replay replay

package mod37

import "strings"

// Record is the unit handled by mod37.
type Record struct {
	Key   string
	Value int
}

// Apply0 processes records for mod37.
func Apply0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Merge1 processes records for mod37.
func Merge1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Load2 processes records for mod37.
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

