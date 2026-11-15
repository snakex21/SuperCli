package audit

import "strings"

// Bucket2 is a bucket record kept by the audit package.
type Bucket2 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight2 returns a stable weight used for ordering bucket records.
func Weight2(r Bucket2) int {
	w := r.Count * 6
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label2 renders a short human readable label.
func Label2(r Bucket2) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed bucket)"
	}
	return r.ID + ": " + name
}

// HasTag2 reports whether the record carries tag t (case-insensitive).
func HasTag2(r Bucket2, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
