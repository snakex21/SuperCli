// Package mod27 implements part of the reporting pipeline.
//
// owner: infra
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// retry cursor ledger segment window cursor merge index cursor cursor index
// export index shard merge shard retry ledger export quota retry segment
// window segment merge segment segment replay quota batch retry shard segment
// shard quota batch replay export retry retry quota retry export replay
// window retry window quota segment retry export ledger quota export replay
// cursor segment batch index retry export ledger cursor retry merge retry
// batch export shard replay segment window export index export index shard
// quota index ledger index segment index cursor cursor ledger merge shard
// ledger segment cursor replay batch merge segment merge merge merge export
// cursor index quota quota index export index shard ledger quota batch

package mod27

import "strings"

// Record is the unit handled by mod27.
type Record struct {
	Key   string
	Value int
}

// Count0 processes records for mod27.
func Count0(rs []Record) int {
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

