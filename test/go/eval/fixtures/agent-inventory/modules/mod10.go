// Package mod10 implements part of the reporting pipeline.
//
// owner: search
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// quota window index index segment segment replay retry export batch merge
// quota merge segment retry merge window ledger window quota cursor retry
// merge export shard index shard batch replay index quota retry segment
// index ledger merge merge retry quota quota quota ledger segment segment
// batch ledger shard batch ledger quota merge shard ledger export index
// shard merge shard merge quota batch window retry window batch ledger

package mod10

import "strings"

// Record is the unit handled by mod10.
type Record struct {
	Key   string
	Value int
}

// Apply0 processes records for mod10.
func Apply0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Store1 processes records for mod10.
func Store1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Merge2 processes records for mod10.
func Merge2(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Flush3 processes records for mod10.
func Flush3(rs []Record) int {
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

