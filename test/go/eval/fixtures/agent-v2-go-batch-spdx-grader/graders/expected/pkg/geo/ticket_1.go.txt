// SPDX-License-Identifier: MIT

package geo

import "strings"

// Ticket1 is a ticket record kept by the geo package.
type Ticket1 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight1 returns a stable weight used for ordering ticket records.
func Weight1(r Ticket1) int {
	w := r.Count * 4
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label1 renders a short human readable label.
func Label1(r Ticket1) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed ticket)"
	}
	return r.ID + ": " + name
}

// HasTag1 reports whether the record carries tag t (case-insensitive).
func HasTag1(r Ticket1, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
