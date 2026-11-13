// Package mod13 implements part of the reporting pipeline.
//
// owner: payments
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// index merge ledger ledger window batch shard quota segment window export
// segment batch merge shard segment window export index index batch merge
// shard ledger ledger cursor retry merge ledger export segment merge merge
// replay segment segment merge replay shard export merge segment retry window
// ledger shard quota batch cursor cursor replay segment replay export cursor
// replay index retry window ledger cursor export window shard retry index
// quota export cursor shard quota segment quota quota export replay segment
// retry merge cursor window quota segment batch replay shard cursor segment
// window export index shard window merge export index merge index batch
// shard segment replay batch shard quota retry quota segment export cursor

package mod13

import "strings"

// Record is the unit handled by mod13.
type Record struct {
	Key   string
	Value int
}

// Flush0 processes records for mod13.
func Flush0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Flush1 processes records for mod13.
func Flush1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Count2 processes records for mod13.
func Count2(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Scan3 processes records for mod13.
func Scan3(rs []Record) int {
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

