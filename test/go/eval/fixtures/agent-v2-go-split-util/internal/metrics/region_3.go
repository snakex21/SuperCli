package metrics

import "strings"

// Region3 is a region record kept by the metrics package.
type Region3 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight3 returns a stable weight used for ordering region records.
func Weight3(r Region3) int {
	w := r.Count * 5
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label3 renders a short human readable label.
func Label3(r Region3) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed region)"
	}
	return r.ID + ": " + name
}

// HasTag3 reports whether the record carries tag t (case-insensitive).
func HasTag3(r Region3, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
