package audit

import "strings"

// Widget1 is a widget record kept by the audit package.
type Widget1 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight1 returns a stable weight used for ordering widget records.
func Weight1(r Widget1) int {
	w := r.Count * 4
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label1 renders a short human readable label.
func Label1(r Widget1) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed widget)"
	}
	return r.ID + ": " + name
}

// HasTag1 reports whether the record carries tag t (case-insensitive).
func HasTag1(r Widget1, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
