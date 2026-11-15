package exports

import "strings"

// Ticket3 is a ticket record kept by the exports package.
type Ticket3 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight3 returns a stable weight used for ordering ticket records.
func Weight3(r Ticket3) int {
	w := r.Count * 6
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label3 renders a short human readable label.
func Label3(r Ticket3) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed ticket)"
	}
	return r.ID + ": " + name
}

// HasTag3 reports whether the record carries tag t (case-insensitive).
func HasTag3(r Ticket3, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
