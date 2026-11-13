// Package mod38 implements part of the reporting pipeline.
//
// owner: search
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// quota cursor index window merge ledger export replay cursor cursor shard
// window quota retry ledger batch batch window shard segment replay shard
// window segment segment window ledger segment cursor ledger cursor retry merge
// replay merge window ledger batch quota cursor replay retry merge replay
// shard shard shard export ledger shard replay retry cursor batch shard
// retry retry window ledger index ledger ledger ledger export quota merge
// replay window quota index retry segment batch ledger replay retry export
// merge window ledger export ledger ledger window index ledger replay ledger
// export quota segment segment retry retry quota index replay segment index
// ledger shard retry merge batch quota cursor segment batch shard quota
// cursor replay shard segment cursor index shard segment shard ledger index
// ledger shard batch merge batch ledger segment merge quota segment shard

package mod38

import "strings"

// Record is the unit handled by mod38.
type Record struct {
	Key   string
	Value int
}

// Scan0 processes records for mod38.
func Scan0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Merge1 processes records for mod38.
func Merge1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Load2 processes records for mod38.
func Load2(rs []Record) int {
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

