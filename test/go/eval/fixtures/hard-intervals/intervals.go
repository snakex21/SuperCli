// Package intervals works with closed integer intervals.
package intervals

import (
	"errors"
	"sort"
)

// ErrInvalid is returned (possibly wrapped) for an interval with Start > End.
var ErrInvalid = errors.New("intervals: invalid interval")

// Interval is the closed range [Start, End]; both ends are included.
type Interval struct {
	Start, End int
}

// Merge returns the union of in as a sorted list of disjoint intervals.
func Merge(in []Interval) ([]Interval, error) {
	// TODO: draft implementation, see task description.
	sort.Slice(in, func(i, j int) bool { return in[i].Start < in[j].Start })
	var out []Interval
	for _, iv := range in {
		if len(out) > 0 && iv.Start <= out[len(out)-1].End {
			if iv.End > out[len(out)-1].End {
				out[len(out)-1].End = iv.End
			}
			continue
		}
		out = append(out, iv)
	}
	return out, nil
}
