package images

import "strings"

// Tenant2 is a tenant record kept by the images package.
type Tenant2 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight2 returns a stable weight used for ordering tenant records.
func Weight2(r Tenant2) int {
	w := r.Count * 6
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label2 renders a short human readable label.
func Label2(r Tenant2) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed tenant)"
	}
	return r.ID + ": " + name
}

// HasTag2 reports whether the record carries tag t (case-insensitive).
func HasTag2(r Tenant2, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
