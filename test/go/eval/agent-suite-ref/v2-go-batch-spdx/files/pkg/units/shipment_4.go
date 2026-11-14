// SPDX-License-Identifier: MIT

package units

import "strings"

// Shipment4 is a shipment record kept by the units package.
type Shipment4 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight4 returns a stable weight used for ordering shipment records.
func Weight4(r Shipment4) int {
	w := r.Count * 4
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label4 renders a short human readable label.
func Label4(r Shipment4) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed shipment)"
	}
	return r.ID + ": " + name
}

// HasTag4 reports whether the record carries tag t (case-insensitive).
func HasTag4(r Shipment4, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
