// Package mod21 implements part of the reporting pipeline.
//
// owner: identity
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// merge index index replay window export segment cursor replay export export
// window quota window shard batch index shard window batch quota replay
// replay window replay replay segment window index window batch replay segment
// window export window quota cursor merge replay batch index shard index
// replay retry batch export batch index ledger quota quota shard index
// quota merge batch segment ledger retry ledger merge quota batch ledger

package mod21

import "strings"

// Record is the unit handled by mod21.
type Record struct {
	Key   string
	Value int
}

// Split0 processes records for mod21.
func Split0(rs []Record) int {
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

