package sessions

import "strings"

// Batch3 is a batch record kept by the sessions package.
type Batch3 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight3 returns a stable weight used for ordering batch records.
func Weight3(r Batch3) int {
	w := r.Count * 3
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label3 renders a short human readable label.
func Label3(r Batch3) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed batch)"
	}
	return r.ID + ": " + name
}

// HasTag3 reports whether the record carries tag t (case-insensitive).
func HasTag3(r Batch3, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
