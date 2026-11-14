package schedule

import "strings"

// Invoice1 is a invoice record kept by the schedule package.
type Invoice1 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight1 returns a stable weight used for ordering invoice records.
func Weight1(r Invoice1) int {
	w := r.Count * 6
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label1 renders a short human readable label.
func Label1(r Invoice1) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed invoice)"
	}
	return r.ID + ": " + name
}

// HasTag1 reports whether the record carries tag t (case-insensitive).
func HasTag1(r Invoice1, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
