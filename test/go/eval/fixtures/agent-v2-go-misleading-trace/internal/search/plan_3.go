package search

import "strings"

// Plan3 is a plan record kept by the search package.
type Plan3 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight3 returns a stable weight used for ordering plan records.
func Weight3(r Plan3) int {
	w := r.Count * 6
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label3 renders a short human readable label.
func Label3(r Plan3) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed plan)"
	}
	return r.ID + ": " + name
}

// HasTag3 reports whether the record carries tag t (case-insensitive).
func HasTag3(r Plan3, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
