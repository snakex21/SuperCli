// SPDX-License-Identifier: MIT

package colors

import "strings"

// Webhook4 is a webhook record kept by the colors package.
type Webhook4 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight4 returns a stable weight used for ordering webhook records.
func Weight4(r Webhook4) int {
	w := r.Count * 6
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label4 renders a short human readable label.
func Label4(r Webhook4) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed webhook)"
	}
	return r.ID + ": " + name
}

// HasTag4 reports whether the record carries tag t (case-insensitive).
func HasTag4(r Webhook4, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
