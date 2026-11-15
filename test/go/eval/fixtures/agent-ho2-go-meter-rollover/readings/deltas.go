package readings

import (
	"fmt"
	"time"
)

// Delta is the consumption between two consecutive readings.
type Delta struct {
	From, To time.Time
	KWh      int64
}

// Deltas returns the consumption between consecutive readings of one
// meter (rs must be sorted by time). A register that passes its maximum
// wraps to zero, so a decrease means exactly one wrap-around: modulus is
// added once. Anything still negative is corrupt data.
func Deltas(rs []Reading, modulus int64) ([]Delta, error) {
	var out []Delta
	for i := 1; i < len(rs); i++ {
		kwh := rs[i].Value - rs[i-1].Value
		if kwh < 0 {
			kwh += modulus
		}
		if kwh < 0 {
			return nil, fmt.Errorf("negative delta %d at %s", kwh, rs[i].At.Format(time.RFC3339))
		}
		out = append(out, Delta{From: rs[i-1].At, To: rs[i].At, KWh: kwh})
	}
	return out, nil
}

// Sum adds up the consumption of ds.
func Sum(ds []Delta) int64 {
	var n int64
	for _, d := range ds {
		n += d.KWh
	}
	return n
}
