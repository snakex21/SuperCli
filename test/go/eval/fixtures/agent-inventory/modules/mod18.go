// Package mod18 implements part of the reporting pipeline.
//
// owner: identity
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// export ledger cursor index cursor quota batch batch retry cursor batch
// index retry ledger segment merge quota export index retry quota replay
// shard cursor batch retry index shard replay merge index merge replay
// export batch index index retry retry cursor quota segment quota index
// quota retry export quota ledger index index batch export merge index
// batch index shard batch merge window index merge export ledger replay
// index retry ledger segment ledger cursor export cursor index cursor segment
// cursor merge index batch ledger batch replay quota merge retry window
// export index window cursor replay shard segment window segment export segment
// export export quota export cursor ledger export export retry cursor shard
// quota replay shard shard replay merge segment retry replay cursor replay
// replay retry quota ledger cursor replay segment window ledger merge window

package mod18

import "strings"

// Record is the unit handled by mod18.
type Record struct {
	Key   string
	Value int
}

// Split0 processes records for mod18.
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

