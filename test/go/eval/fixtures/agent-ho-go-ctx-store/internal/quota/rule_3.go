package quota

import "strings"

// Rule3 is a rule record kept by the quota package.
type Rule3 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight3 returns a stable weight used for ordering rule records.
func Weight3(r Rule3) int {
	w := r.Count * 7
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label3 renders a short human readable label.
func Label3(r Rule3) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed rule)"
	}
	return r.ID + ": " + name
}

// HasTag3 reports whether the record carries tag t (case-insensitive).
func HasTag3(r Rule3, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
