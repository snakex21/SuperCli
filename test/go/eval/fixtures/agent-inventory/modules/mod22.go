// Package mod22 implements part of the reporting pipeline.
//
// owner: search
// status: deprecated
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// shard retry replay export replay cursor index merge quota window merge
// export replay batch index quota window ledger replay shard export index
// ledger window ledger cursor quota retry export batch merge batch batch
// window window cursor quota export replay shard export shard retry cursor
// segment cursor window merge segment export retry retry batch ledger cursor
// replay retry ledger batch replay replay batch export cursor replay merge
// ledger replay replay merge ledger index ledger window segment cursor cursor
// index retry ledger export retry quota retry batch shard quota index
// export quota batch ledger retry segment cursor ledger export ledger batch

package mod22

import "strings"

// Record is the unit handled by mod22.
type Record struct {
	Key   string
	Value int
}

// Split0 processes records for mod22.
func Split0(rs []Record) int {
	n := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Key, "x") {
			n += r.Value
		}
	}
	return n
}

// Load1 processes records for mod22.
func Load1(rs []Record) int {
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

