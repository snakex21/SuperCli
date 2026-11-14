package audit

import "strings"

// Bucket4 is a bucket record kept by the audit package.
type Bucket4 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight4 returns a stable weight used for ordering bucket records.
func Weight4(r Bucket4) int {
	w := r.Count * 8
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label4 renders a short human readable label.
func Label4(r Bucket4) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed bucket)"
	}
	return r.ID + ": " + name
}

// HasTag4 reports whether the record carries tag t (case-insensitive).
func HasTag4(r Bucket4, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
