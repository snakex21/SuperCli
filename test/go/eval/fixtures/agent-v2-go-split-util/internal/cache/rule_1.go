package cache

import "strings"

// Rule1 is a rule record kept by the cache package.
type Rule1 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight1 returns a stable weight used for ordering rule records.
func Weight1(r Rule1) int {
	w := r.Count * 5
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label1 renders a short human readable label.
func Label1(r Rule1) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed rule)"
	}
	return r.ID + ": " + name
}

// HasTag1 reports whether the record carries tag t (case-insensitive).
func HasTag1(r Rule1, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
