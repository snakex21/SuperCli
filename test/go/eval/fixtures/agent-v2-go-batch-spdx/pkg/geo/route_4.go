package geo

import "strings"

// Route4 is a route record kept by the geo package.
type Route4 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight4 returns a stable weight used for ordering route records.
func Weight4(r Route4) int {
	w := r.Count * 8
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label4 renders a short human readable label.
func Label4(r Route4) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed route)"
	}
	return r.ID + ": " + name
}

// HasTag4 reports whether the record carries tag t (case-insensitive).
func HasTag4(r Route4, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
