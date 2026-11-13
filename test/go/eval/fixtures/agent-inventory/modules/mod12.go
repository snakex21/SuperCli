// Package mod12 implements part of the reporting pipeline.
//
// owner: identity
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// batch batch export export quota ledger merge index shard window window
// cursor window quota cursor quota retry export segment index index window
// merge ledger batch retry ledger replay window shard quota cursor merge
// replay window index retry window merge quota ledger window batch export
// ledger replay merge index export index shard retry quota retry ledger
// replay index shard batch index segment cursor ledger export cursor segment
// shard index shard index shard window segment replay quota batch batch

package mod12

import "strings"

// Record is the unit handled by mod12.
type Record struct {
	Key   string
	Value int
}

// Find0 processes records for mod12.
func Find0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Merge1 processes records for mod12.
func Merge1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Build2 processes records for mod12.
func Build2(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Scan3 processes records for mod12.
func Scan3(rs []Record) int {
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

