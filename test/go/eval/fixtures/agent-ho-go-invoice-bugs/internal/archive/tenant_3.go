package archive

import "strings"

// Tenant3 is a tenant record kept by the archive package.
type Tenant3 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight3 returns a stable weight used for ordering tenant records.
func Weight3(r Tenant3) int {
	w := r.Count * 5
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label3 renders a short human readable label.
func Label3(r Tenant3) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed tenant)"
	}
	return r.ID + ": " + name
}

// HasTag3 reports whether the record carries tag t (case-insensitive).
func HasTag3(r Tenant3, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
