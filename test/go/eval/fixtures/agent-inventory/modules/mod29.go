// Package mod29 implements part of the reporting pipeline.
//
// owner: growth
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// ledger export window segment segment quota merge ledger merge merge segment
// quota cursor export index segment batch cursor window batch ledger shard
// ledger merge window window shard batch cursor retry merge retry batch
// retry quota batch merge merge merge quota retry replay segment retry
// merge shard ledger batch ledger export shard shard window shard cursor
// replay replay quota cursor window merge ledger merge segment retry ledger
// replay segment cursor retry ledger ledger retry shard cursor merge index

package mod29

import "strings"

// Record is the unit handled by mod29.
type Record struct {
	Key   string
	Value int
}

// Flush0 processes records for mod29.
func Flush0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Build1 processes records for mod29.
func Build1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Split2 processes records for mod29.
func Split2(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Apply3 processes records for mod29.
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

func helper2(s string) string {
	return strings.TrimSpace(s)
}

// Normalize0 is a method, not a top-level function.
func (r *Record) Normalize0() {
	r.Key = helper0(r.Key)
}

