package archive

import "strings"

// Shipment2 is a shipment record kept by the archive package.
type Shipment2 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight2 returns a stable weight used for ordering shipment records.
func Weight2(r Shipment2) int {
	w := r.Count * 7
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label2 renders a short human readable label.
func Label2(r Shipment2) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed shipment)"
	}
	return r.ID + ": " + name
}

// HasTag2 reports whether the record carries tag t (case-insensitive).
func HasTag2(r Shipment2, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
