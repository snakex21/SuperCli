// Package mod04 implements part of the reporting pipeline.
//
// owner: search
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// cursor shard segment shard segment index quota window ledger segment ledger
// export window ledger index batch retry replay window index index ledger
// quota retry segment shard ledger segment retry window segment segment ledger
// batch export segment export batch batch ledger cursor batch segment ledger
// ledger index quota shard retry retry merge shard index index replay
// shard batch export retry shard cursor shard merge export index ledger
// index retry quota index segment export segment retry batch replay replay
// export batch segment replay retry segment replay ledger segment export retry
// shard batch quota retry cursor quota batch replay ledger window shard
// ledger ledger batch window quota retry shard index index segment batch
// export shard window retry export export export cursor export index cursor

package mod04

import "strings"

// Record is the unit handled by mod04.
type Record struct {
	Key   string
	Value int
}

// Merge0 processes records for mod04.
func Merge0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Merge1 processes records for mod04.
func Merge1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Flush2 processes records for mod04.
func Flush2(rs []Record) int {
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

