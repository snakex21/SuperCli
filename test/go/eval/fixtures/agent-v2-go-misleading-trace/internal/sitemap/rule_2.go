package sitemap

import "strings"

// Rule2 is a rule record kept by the sitemap package.
type Rule2 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight2 returns a stable weight used for ordering rule records.
func Weight2(r Rule2) int {
	w := r.Count * 8
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label2 renders a short human readable label.
func Label2(r Rule2) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed rule)"
	}
	return r.ID + ": " + name
}

// HasTag2 reports whether the record carries tag t (case-insensitive).
func HasTag2(r Rule2, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
