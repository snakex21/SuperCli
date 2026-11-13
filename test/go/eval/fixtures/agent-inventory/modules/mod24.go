// Package mod24 implements part of the reporting pipeline.
//
// owner: search
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// batch segment shard export retry merge batch shard export batch retry
// export segment replay cursor cursor cursor quota merge index shard window
// export quota merge merge ledger batch shard shard export segment segment
// segment export shard segment batch cursor merge window batch retry export
// quota cursor window export export index batch merge retry export quota
// shard export retry ledger export replay segment quota batch merge quota
// quota window batch index cursor shard export window window batch shard
// index quota merge export shard retry cursor replay replay shard merge

package mod24

import "strings"

// Record is the unit handled by mod24.
type Record struct {
	Key   string
	Value int
}

// Store0 processes records for mod24.
func Store0(rs []Record) int {
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

func helper2(s string) string {
	return strings.TrimSpace(s)
}

// Normalize0 is a method, not a top-level function.
func (r *Record) Normalize0() {
	r.Key = helper0(r.Key)
}

