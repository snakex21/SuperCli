// Package mod08 implements part of the reporting pipeline.
//
// owner: growth
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// window ledger batch segment export batch window quota replay quota cursor
// merge index index retry retry retry retry shard merge segment merge
// retry segment batch index merge window batch merge retry segment merge
// cursor retry segment quota replay export segment cursor shard batch export
// ledger replay replay index index quota index quota segment merge shard
// ledger quota quota shard retry merge retry index retry retry window
// ledger quota window segment quota quota export index ledger shard cursor
// batch merge cursor cursor export quota quota window quota cursor segment
// replay merge window quota index retry retry index export quota shard
// replay quota cursor window shard quota replay window replay window window

package mod08

import "strings"

// Record is the unit handled by mod08.
type Record struct {
	Key   string
	Value int
}

// Scan0 processes records for mod08.
func Scan0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Load1 processes records for mod08.
func Load1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Load2 processes records for mod08.
func Load2(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Count3 processes records for mod08.
func Count3(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Count4 processes records for mod08.
func Count4(rs []Record) int {
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

