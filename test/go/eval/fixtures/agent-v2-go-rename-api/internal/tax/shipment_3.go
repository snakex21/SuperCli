package tax

import "strings"

// Shipment3 is a shipment record kept by the tax package.
type Shipment3 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight3 returns a stable weight used for ordering shipment records.
func Weight3(r Shipment3) int {
	w := r.Count * 2
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label3 renders a short human readable label.
func Label3(r Shipment3) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed shipment)"
	}
	return r.ID + ": " + name
}

// HasTag3 reports whether the record carries tag t (case-insensitive).
func HasTag3(r Shipment3, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
