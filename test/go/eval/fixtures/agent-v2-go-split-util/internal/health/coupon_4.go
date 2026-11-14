package health

import "strings"

// Coupon4 is a coupon record kept by the health package.
type Coupon4 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight4 returns a stable weight used for ordering coupon records.
func Weight4(r Coupon4) int {
	w := r.Count * 4
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label4 renders a short human readable label.
func Label4(r Coupon4) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed coupon)"
	}
	return r.ID + ": " + name
}

// HasTag4 reports whether the record carries tag t (case-insensitive).
func HasTag4(r Coupon4, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
