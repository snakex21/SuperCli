// Package mod11 implements part of the reporting pipeline.
//
// owner: identity
// status: active
//
// Notes: this package is generated from the legacy catalogue. The text below
// documents historical behaviour that callers still rely on; it is kept
// verbatim so that reviews can diff it against the catalogue export.
// shard quota ledger replay retry replay window segment merge cursor retry
// replay merge replay cursor export cursor quota retry export batch merge
// cursor shard window merge cursor ledger shard merge shard export merge
// quota batch merge cursor export replay batch quota merge window segment
// cursor quota retry segment export replay shard batch retry shard quota
// window quota ledger shard retry cursor shard ledger quota window merge
// export ledger ledger export export batch cursor cursor batch segment window
// quota segment retry ledger segment window batch cursor replay index replay
// quota ledger merge quota index batch retry replay merge quota index
// batch cursor index index merge batch segment ledger window index ledger
// segment cursor batch segment shard index batch merge segment ledger replay
// replay quota window quota index quota shard quota cursor merge batch

package mod11

import "strings"

// Record is the unit handled by mod11.
type Record struct {
	Key   string
	Value int
}

// Flush0 processes records for mod11.
func Flush0(rs []Record) int {
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

