// Package mod40 implements part of the reporting pipeline.
//
// owner: payments
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// export batch export export ledger cursor window segment shard replay export
// index retry replay export index index replay merge shard shard retry
// index ledger merge shard segment export segment cursor window ledger replay
// batch merge merge window replay retry merge retry retry index batch
// shard batch batch quota cursor batch cursor segment retry segment shard
// cursor retry window shard merge retry batch merge merge merge cursor
// shard segment segment batch retry ledger segment replay cursor quota ledger

package mod40

import "strings"

// Record is the unit handled by mod40.
type Record struct {
	Key   string
	Value int
}

// Find0 processes records for mod40.
func Find0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Scan1 processes records for mod40.
func Scan1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Apply2 processes records for mod40.
func Apply2(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Count3 processes records for mod40.
func Count3(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Store4 processes records for mod40.
func Store4(rs []Record) int {
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

