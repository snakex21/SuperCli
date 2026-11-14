package storage

import "strings"

// Widget4 is a widget record kept by the storage package.
type Widget4 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight4 returns a stable weight used for ordering widget records.
func Weight4(r Widget4) int {
	w := r.Count * 2
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label4 renders a short human readable label.
func Label4(r Widget4) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed widget)"
	}
	return r.ID + ": " + name
}

// HasTag4 reports whether the record carries tag t (case-insensitive).
func HasTag4(r Widget4, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
