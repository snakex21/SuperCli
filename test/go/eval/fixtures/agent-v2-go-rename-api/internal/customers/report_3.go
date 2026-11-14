package customers

import "strings"

// Report3 is a report record kept by the customers package.
type Report3 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight3 returns a stable weight used for ordering report records.
func Weight3(r Report3) int {
	w := r.Count * 4
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label3 renders a short human readable label.
func Label3(r Report3) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed report)"
	}
	return r.ID + ": " + name
}

// HasTag3 reports whether the record carries tag t (case-insensitive).
func HasTag3(r Report3, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
