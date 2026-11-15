package quota

import "strings"

// Profile2 is a profile record kept by the quota package.
type Profile2 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight2 returns a stable weight used for ordering profile records.
func Weight2(r Profile2) int {
	w := r.Count * 3
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label2 renders a short human readable label.
func Label2(r Profile2) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed profile)"
	}
	return r.ID + ": " + name
}

// HasTag2 reports whether the record carries tag t (case-insensitive).
func HasTag2(r Profile2, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
