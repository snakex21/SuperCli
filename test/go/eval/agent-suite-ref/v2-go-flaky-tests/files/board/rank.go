// Package board ranks players.
package board

import "sort"

// Entry is one ranked player.
type Entry struct {
	Name  string
	Score int
}

// Rank returns all players ordered by score, highest first. Players
// with equal scores are ordered by name (ascending).
func Rank(scores map[string]int) []Entry {
	out := make([]Entry, 0, len(scores))
	for name, s := range scores {
		out = append(out, Entry{Name: name, Score: s})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Top returns the first n entries of Rank.
func Top(scores map[string]int, n int) []Entry {
	r := Rank(scores)
	if n < len(r) {
		r = r[:n]
	}
	return r
}
