package users

import "strings"

// Quota1 is a quota record kept by the users package.
type Quota1 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight1 returns a stable weight used for ordering quota records.
func Weight1(r Quota1) int {
	w := r.Count * 4
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label1 renders a short human readable label.
func Label1(r Quota1) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed quota)"
	}
	return r.ID + ": " + name
}

// HasTag1 reports whether the record carries tag t (case-insensitive).
func HasTag1(r Quota1, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
