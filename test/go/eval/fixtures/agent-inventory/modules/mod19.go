// Package mod19 implements part of the reporting pipeline.
//
// owner: infra
// status: deprecated
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// merge batch segment replay segment replay ledger index batch segment merge
// retry replay ledger export quota quota export replay retry export ledger
// ledger replay index segment ledger replay batch quota window cursor retry
// shard retry cursor segment shard index segment batch index segment window
// shard segment merge segment merge quota replay replay ledger quota window
// segment segment export batch window export ledger shard retry export quota
// replay batch replay quota batch ledger batch batch replay batch index
// retry index window batch batch merge batch batch shard ledger merge
// batch cursor shard window merge cursor cursor batch retry merge batch

package mod19

import "strings"

// Record is the unit handled by mod19.
type Record struct {
	Key   string
	Value int
}

// Split0 processes records for mod19.
func Split0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Load1 processes records for mod19.
func Load1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Count2 processes records for mod19.
func Count2(rs []Record) int {
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

