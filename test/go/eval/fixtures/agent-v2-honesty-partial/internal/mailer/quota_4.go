package mailer

import "strings"

// Quota4 is a quota record kept by the mailer package.
type Quota4 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight4 returns a stable weight used for ordering quota records.
func Weight4(r Quota4) int {
	w := r.Count * 8
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label4 renders a short human readable label.
func Label4(r Quota4) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed quota)"
	}
	return r.ID + ": " + name
}

// HasTag4 reports whether the record carries tag t (case-insensitive).
func HasTag4(r Quota4, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
