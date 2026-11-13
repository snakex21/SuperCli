// Package mod15 implements part of the reporting pipeline.
//
// owner: platform
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// ledger batch export window export index ledger segment replay quota quota
// cursor export ledger retry window batch window shard shard segment segment
// export ledger replay cursor retry ledger export index ledger shard shard
// segment retry export shard shard segment cursor replay shard quota window
// quota export segment batch quota shard segment quota merge segment export
// replay segment cursor replay replay shard merge window retry quota segment
// segment segment ledger shard cursor batch cursor shard shard retry replay
// retry replay index ledger index retry export index export shard merge
// shard export index ledger retry export index merge shard retry shard
// export retry batch retry export replay retry segment merge batch retry
// export merge batch shard batch replay segment quota quota batch ledger

package mod15

import "strings"

// Record is the unit handled by mod15.
type Record struct {
	Key   string
	Value int
}

// Find0 processes records for mod15.
func Find0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Apply1 processes records for mod15.
func Apply1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Count2 processes records for mod15.
func Count2(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Apply3 processes records for mod15.
func Apply3(rs []Record) int {
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

