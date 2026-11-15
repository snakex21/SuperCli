package reminders

import "strings"

// Invoice4 is a invoice record kept by the reminders package.
type Invoice4 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight4 returns a stable weight used for ordering invoice records.
func Weight4(r Invoice4) int {
	w := r.Count * 2
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label4 renders a short human readable label.
func Label4(r Invoice4) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed invoice)"
	}
	return r.ID + ": " + name
}

// HasTag4 reports whether the record carries tag t (case-insensitive).
func HasTag4(r Invoice4, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
