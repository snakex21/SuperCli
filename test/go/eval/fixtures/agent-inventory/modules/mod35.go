// Package mod35 implements part of the reporting pipeline.
//
// owner: platform
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// index segment batch shard shard cursor replay merge shard replay merge
// segment shard export window window segment retry replay index shard batch
// batch segment merge ledger quota segment index replay quota batch merge
// quota window export shard retry retry batch segment cursor shard merge
// segment segment batch window retry window ledger shard export batch merge
// export cursor quota export retry cursor retry ledger replay shard batch
// segment merge batch quota merge export ledger replay cursor merge retry
// batch segment shard index shard merge replay shard ledger export window
// index window export replay window segment shard window ledger index merge
// index batch merge quota quota replay merge segment retry replay segment

package mod35

import "strings"

// Record is the unit handled by mod35.
type Record struct {
	Key   string
	Value int
}

// Scan0 processes records for mod35.
func Scan0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Merge1 processes records for mod35.
func Merge1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Flush2 processes records for mod35.
func Flush2(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Count3 processes records for mod35.
func Count3(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Find4 processes records for mod35.
func Find4(rs []Record) int {
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

