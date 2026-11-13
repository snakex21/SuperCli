// Package mod07 implements part of the reporting pipeline.
//
// owner: infra
// status: experimental
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// cursor window merge replay shard index replay window export segment retry
// ledger retry segment merge segment window quota index segment cursor retry
// window quota ledger segment segment shard merge index export export batch
// quota replay retry index export retry ledger index merge index index
// index quota quota cursor export retry index retry batch export replay
// window batch segment quota replay segment window retry shard index shard
// export merge merge retry batch batch cursor index ledger index segment
// retry batch cursor merge quota segment shard index retry ledger quota
// replay cursor quota batch ledger window retry cursor merge cursor shard

package mod07

import "strings"

// Record is the unit handled by mod07.
type Record struct {
	Key   string
	Value int
}

// Merge0 processes records for mod07.
func Merge0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Count1 processes records for mod07.
func Count1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Store2 processes records for mod07.
func Store2(rs []Record) int {
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

