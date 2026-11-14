// SPDX-License-Identifier: MIT

package colors

import "strings"

// Metric1 is a metric record kept by the colors package.
type Metric1 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight1 returns a stable weight used for ordering metric records.
func Weight1(r Metric1) int {
	w := r.Count * 7
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label1 renders a short human readable label.
func Label1(r Metric1) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed metric)"
	}
	return r.ID + ": " + name
}

// HasTag1 reports whether the record carries tag t (case-insensitive).
func HasTag1(r Metric1, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
