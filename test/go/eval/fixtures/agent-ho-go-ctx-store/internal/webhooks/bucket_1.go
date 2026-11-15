package webhooks

import "strings"

// Bucket1 is a bucket record kept by the webhooks package.
type Bucket1 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight1 returns a stable weight used for ordering bucket records.
func Weight1(r Bucket1) int {
	w := r.Count * 3
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label1 renders a short human readable label.
func Label1(r Bucket1) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed bucket)"
	}
	return r.ID + ": " + name
}

// HasTag1 reports whether the record carries tag t (case-insensitive).
func HasTag1(r Bucket1, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
