// Package mod23 implements part of the reporting pipeline.
//
// owner: search
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// window batch retry replay ledger ledger segment quota window shard index
// segment ledger quota cursor segment ledger shard batch quota window cursor
// quota export replay index ledger cursor retry window replay ledger replay
// window window window cursor replay quota index replay export cursor merge
// quota merge merge export batch segment replay batch retry segment window
// segment export quota replay retry segment index window segment window merge
// export segment ledger index replay window shard index merge segment segment
// replay index merge export export batch segment replay merge ledger cursor

package mod23

import "strings"

// Record is the unit handled by mod23.
type Record struct {
	Key   string
	Value int
}

// Scan0 processes records for mod23.
func Scan0(rs []Record) int {
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

