// Package mod34 implements part of the reporting pipeline.
//
// owner: identity
// status: deprecated
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// index cursor batch segment segment export ledger quota ledger ledger quota
// cursor cursor ledger merge quota window batch retry segment batch index
// replay index segment shard ledger retry cursor window ledger export index
// quota merge batch merge quota replay segment segment replay index cursor
// index replay ledger merge cursor export merge retry ledger window merge
// index window retry retry index retry replay index export retry ledger
// merge segment merge window replay export batch batch ledger window batch
// index window export export window merge shard merge cursor export merge
// index export export export shard merge ledger export shard index merge

package mod34

import "strings"

// Record is the unit handled by mod34.
type Record struct {
	Key   string
	Value int
}

// Split0 processes records for mod34.
func Split0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Store1 processes records for mod34.
func Store1(rs []Record) int {
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

