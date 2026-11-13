// Package mod16 implements part of the reporting pipeline.
//
// owner: identity
// status: experimental
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// batch ledger cursor window batch replay retry shard quota merge ledger
// index shard quota quota index shard batch segment window ledger export
// cursor ledger replay ledger ledger ledger shard replay replay export batch
// window merge quota replay batch segment export retry quota index merge
// cursor batch export segment window segment index quota window shard export
// replay batch ledger merge shard export merge merge index index window
// batch segment ledger window export cursor segment replay merge quota shard

package mod16

import "strings"

// Record is the unit handled by mod16.
type Record struct {
	Key   string
	Value int
}

// Merge0 processes records for mod16.
func Merge0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Split1 processes records for mod16.
func Split1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Find2 processes records for mod16.
func Find2(rs []Record) int {
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

