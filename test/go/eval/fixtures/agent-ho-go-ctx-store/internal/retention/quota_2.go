package retention

import "strings"

// Quota2 is a quota record kept by the retention package.
type Quota2 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight2 returns a stable weight used for ordering quota records.
func Weight2(r Quota2) int {
	w := r.Count * 8
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label2 renders a short human readable label.
func Label2(r Quota2) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed quota)"
	}
	return r.ID + ": " + name
}

// HasTag2 reports whether the record carries tag t (case-insensitive).
func HasTag2(r Quota2, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
