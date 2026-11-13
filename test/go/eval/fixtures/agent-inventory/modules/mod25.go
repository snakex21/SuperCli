// Package mod25 implements part of the reporting pipeline.
//
// owner: payments
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// merge export index batch batch window quota window window batch merge
// batch ledger index export shard window shard quota segment segment window
// merge quota window batch replay export replay window segment segment cursor
// ledger merge merge segment export export segment cursor window index cursor
// ledger export replay merge merge merge shard window ledger segment ledger
// replay retry cursor segment segment merge index shard index export merge
// window index retry window ledger quota shard shard batch cursor cursor
// window ledger window replay export replay export quota export shard window
// window merge ledger cursor quota cursor ledger window shard segment replay
// merge ledger retry index batch index window index replay shard retry
// window shard window batch merge segment window retry export retry merge
// shard quota cursor merge ledger merge replay export cursor segment retry

package mod25

import "strings"

// Record is the unit handled by mod25.
type Record struct {
	Key   string
	Value int
}

// Merge0 processes records for mod25.
func Merge0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Apply1 processes records for mod25.
func Apply1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Load2 processes records for mod25.
func Load2(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Apply3 processes records for mod25.
func Apply3(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Count4 processes records for mod25.
func Count4(rs []Record) int {
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

// Normalize1 is a method, not a top-level function.
func (r *Record) Normalize1() {
	r.Key = helper0(r.Key)
}

