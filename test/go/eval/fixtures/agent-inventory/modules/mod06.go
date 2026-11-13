// Package mod06 implements part of the reporting pipeline.
//
// owner: payments
// status: deprecated
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// quota segment retry window export cursor replay index batch shard index
// merge export retry quota window replay quota batch window retry quota
// window window batch ledger quota index replay index batch retry ledger
// retry segment cursor quota window shard shard cursor window ledger retry
// merge ledger replay ledger export segment shard segment index ledger retry
// merge merge ledger quota quota cursor retry index merge shard retry
// ledger ledger ledger merge segment segment index cursor window retry export
// quota quota retry retry replay index merge export retry window cursor

package mod06

import "strings"

// Record is the unit handled by mod06.
type Record struct {
	Key   string
	Value int
}

// Build0 processes records for mod06.
func Build0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Split1 processes records for mod06.
func Split1(rs []Record) int {
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

