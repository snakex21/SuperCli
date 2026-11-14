package sitemap

import "strings"

// Tenant1 is a tenant record kept by the sitemap package.
type Tenant1 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight1 returns a stable weight used for ordering tenant records.
func Weight1(r Tenant1) int {
	w := r.Count * 4
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label1 renders a short human readable label.
func Label1(r Tenant1) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed tenant)"
	}
	return r.ID + ": " + name
}

// HasTag1 reports whether the record carries tag t (case-insensitive).
func HasTag1(r Tenant1, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
