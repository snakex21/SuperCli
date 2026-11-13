// Package mod31 implements part of the reporting pipeline.
//
// owner: infra
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// shard segment export batch retry retry ledger index quota cursor batch
// batch index batch merge ledger segment export retry retry retry merge
// window replay window cursor segment quota quota quota cursor batch window
// window export replay retry segment shard batch quota shard quota window
// cursor cursor quota segment cursor batch retry shard replay batch replay
// export ledger shard window window merge retry retry cursor retry segment
// quota cursor segment segment merge segment segment index retry ledger shard
// shard retry replay retry shard index cursor replay shard window shard
// quota ledger merge batch replay cursor replay export ledger index cursor
// replay quota merge cursor batch index cursor merge retry window export
// quota batch retry merge quota export batch retry replay ledger index

package mod31

import "strings"

// Record is the unit handled by mod31.
type Record struct {
	Key   string
	Value int
}

// Flush0 processes records for mod31.
func Flush0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Split1 processes records for mod31.
func Split1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Build2 processes records for mod31.
func Build2(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Apply3 processes records for mod31.
func Apply3(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Store4 processes records for mod31.
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

