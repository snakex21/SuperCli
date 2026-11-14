package i18n

import "strings"

// Widget2 is a widget record kept by the i18n package.
type Widget2 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight2 returns a stable weight used for ordering widget records.
func Weight2(r Widget2) int {
	w := r.Count * 7
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label2 renders a short human readable label.
func Label2(r Widget2) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed widget)"
	}
	return r.ID + ": " + name
}

// HasTag2 reports whether the record carries tag t (case-insensitive).
func HasTag2(r Widget2, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
