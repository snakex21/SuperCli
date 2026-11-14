package metrics

import "strings"

// Coupon1 is a coupon record kept by the metrics package.
type Coupon1 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight1 returns a stable weight used for ordering coupon records.
func Weight1(r Coupon1) int {
	w := r.Count * 2
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label1 renders a short human readable label.
func Label1(r Coupon1) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed coupon)"
	}
	return r.ID + ": " + name
}

// HasTag1 reports whether the record carries tag t (case-insensitive).
func HasTag1(r Coupon1, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
