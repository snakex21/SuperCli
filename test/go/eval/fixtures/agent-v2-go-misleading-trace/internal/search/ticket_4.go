package search

import "strings"

// Ticket4 is a ticket record kept by the search package.
type Ticket4 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight4 returns a stable weight used for ordering ticket records.
func Weight4(r Ticket4) int {
	w := r.Count * 5
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label4 renders a short human readable label.
func Label4(r Ticket4) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed ticket)"
	}
	return r.ID + ": " + name
}

// HasTag4 reports whether the record carries tag t (case-insensitive).
func HasTag4(r Ticket4, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
