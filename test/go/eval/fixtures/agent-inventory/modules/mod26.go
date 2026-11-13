// Package mod26 implements part of the reporting pipeline.
//
// owner: identity
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// shard segment export batch shard export retry index quota segment retry
// replay batch batch merge index segment replay quota retry ledger segment
// quota ledger batch ledger cursor cursor ledger retry export batch segment
// index batch quota replay cursor merge quota replay ledger shard retry
// index replay shard export ledger batch ledger batch ledger shard replay
// retry replay segment segment segment export ledger retry ledger retry merge
// retry replay index cursor shard index index window ledger batch index
// window retry segment shard replay batch index shard window index ledger

package mod26

import "strings"

// Record is the unit handled by mod26.
type Record struct {
	Key   string
	Value int
}

// Flush0 processes records for mod26.
func Flush0(rs []Record) int {
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

