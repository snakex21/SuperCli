// Package mod05 implements part of the reporting pipeline.
//
// owner: payments
// status: deprecated
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// segment window cursor batch batch segment quota merge export segment retry
// window index shard ledger window quota batch index quota segment retry
// index quota replay retry batch index batch index cursor merge batch
// cursor index quota window replay export replay retry cursor quota segment
// retry cursor retry retry merge index merge quota merge index shard
// segment cursor window index export window merge batch retry quota replay
// export batch index window ledger segment retry quota replay segment window
// replay replay ledger batch quota cursor replay shard segment shard batch
// export export quota quota index index window quota ledger ledger window

package mod05

import "strings"

// Record is the unit handled by mod05.
type Record struct {
	Key   string
	Value int
}

// Apply0 processes records for mod05.
func Apply0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Find1 processes records for mod05.
func Find1(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Flush2 processes records for mod05.
func Flush2(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Count3 processes records for mod05.
func Count3(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Count4 processes records for mod05.
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

