package i18n

import "strings"

// Profile1 is a profile record kept by the i18n package.
type Profile1 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight1 returns a stable weight used for ordering profile records.
func Weight1(r Profile1) int {
	w := r.Count * 3
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label1 renders a short human readable label.
func Label1(r Profile1) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed profile)"
	}
	return r.ID + ": " + name
}

// HasTag1 reports whether the record carries tag t (case-insensitive).
func HasTag1(r Profile1, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
