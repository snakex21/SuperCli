package schedule

import "strings"

// Metric4 is a metric record kept by the schedule package.
type Metric4 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight4 returns a stable weight used for ordering metric records.
func Weight4(r Metric4) int {
	w := r.Count * 9
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label4 renders a short human readable label.
func Label4(r Metric4) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed metric)"
	}
	return r.ID + ": " + name
}

// HasTag4 reports whether the record carries tag t (case-insensitive).
func HasTag4(r Metric4, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
