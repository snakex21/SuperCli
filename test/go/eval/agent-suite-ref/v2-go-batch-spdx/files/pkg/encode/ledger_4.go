// SPDX-License-Identifier: MIT

package encode

import "strings"

// Ledger4 is a ledger record kept by the encode package.
type Ledger4 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight4 returns a stable weight used for ordering ledger records.
func Weight4(r Ledger4) int {
	w := r.Count * 4
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label4 renders a short human readable label.
func Label4(r Ledger4) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed ledger)"
	}
	return r.ID + ": " + name
}

// HasTag4 reports whether the record carries tag t (case-insensitive).
func HasTag4(r Ledger4, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
