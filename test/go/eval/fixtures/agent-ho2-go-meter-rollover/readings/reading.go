// Package readings holds register readings and turns them into consumption.
package readings

import (
	"sort"
	"time"
)

// Reading is one register value of a meter at a point in time (kWh).
type Reading struct {
	Meter string
	At    time.Time
	Value int64
}

// SortByTime orders readings chronologically (stable for equal times).
func SortByTime(rs []Reading) {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].At.Before(rs[j].At) })
}

// ByMeter groups readings by meter id.
func ByMeter(rs []Reading) map[string][]Reading {
	out := map[string][]Reading{}
	for _, r := range rs {
		out[r.Meter] = append(out[r.Meter], r)
	}
	return out
}
