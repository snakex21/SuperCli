package feeds

import "strings"

// Rule4 is a rule record kept by the feeds package.
type Rule4 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight4 returns a stable weight used for ordering rule records.
func Weight4(r Rule4) int {
	w := r.Count * 6
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label4 renders a short human readable label.
func Label4(r Rule4) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed rule)"
	}
	return r.ID + ": " + name
}

// HasTag4 reports whether the record carries tag t (case-insensitive).
func HasTag4(r Rule4, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
