// Package mod39 implements part of the reporting pipeline.
//
// owner: identity
// status: experimental
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// ledger quota batch retry index cursor retry replay batch ledger shard
// ledger shard export retry window batch batch cursor index segment export
// quota segment window batch cursor segment batch merge export segment ledger
// retry window segment shard merge merge batch retry cursor cursor export
// retry export window index replay batch batch ledger ledger batch quota
// merge retry cursor batch merge batch replay batch replay index ledger
// retry replay cursor merge shard ledger batch ledger merge window merge
// ledger merge window window segment replay retry shard shard segment shard
// batch segment retry window shard replay cursor index ledger cursor ledger
// cursor shard batch batch replay index replay window export segment replay

package mod39

import "strings"

// Record is the unit handled by mod39.
type Record struct {
	Key   string
	Value int
}

// Count0 processes records for mod39.
func Count0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Load1 processes records for mod39.
func Load1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Flush2 processes records for mod39.
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

