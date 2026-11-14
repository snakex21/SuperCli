package matches

import "strings"

// Profile4 is a profile record kept by the matches package.
type Profile4 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight4 returns a stable weight used for ordering profile records.
func Weight4(r Profile4) int {
	w := r.Count * 9
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label4 renders a short human readable label.
func Label4(r Profile4) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed profile)"
	}
	return r.ID + ": " + name
}

// HasTag4 reports whether the record carries tag t (case-insensitive).
func HasTag4(r Profile4, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
