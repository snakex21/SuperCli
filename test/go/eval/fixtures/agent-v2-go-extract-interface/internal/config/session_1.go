package config

import "strings"

// Session1 is a session record kept by the config package.
type Session1 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight1 returns a stable weight used for ordering session records.
func Weight1(r Session1) int {
	w := r.Count * 8
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label1 renders a short human readable label.
func Label1(r Session1) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed session)"
	}
	return r.ID + ": " + name
}

// HasTag1 reports whether the record carries tag t (case-insensitive).
func HasTag1(r Session1, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
