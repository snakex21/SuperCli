package audit

import "strings"

// Ledger2 is a ledger record kept by the audit package.
type Ledger2 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight2 returns a stable weight used for ordering ledger records.
func Weight2(r Ledger2) int {
	w := r.Count * 9
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label2 renders a short human readable label.
func Label2(r Ledger2) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed ledger)"
	}
	return r.ID + ": " + name
}

// HasTag2 reports whether the record carries tag t (case-insensitive).
func HasTag2(r Ledger2, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
