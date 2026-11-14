package feeds

import "strings"

// Profile3 is a profile record kept by the feeds package.
type Profile3 struct {
	ID    string
	Name  string
	Count int
	Tags  []string
}

// Weight3 returns a stable weight used for ordering profile records.
func Weight3(r Profile3) int {
	w := r.Count * 9
	for _, t := range r.Tags {
		w += len(t)
	}
	return w
}

// Label3 renders a short human readable label.
func Label3(r Profile3) string {
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "(unnamed profile)"
	}
	return r.ID + ": " + name
}

// HasTag3 reports whether the record carries tag t (case-insensitive).
func HasTag3(r Profile3, t string) bool {
	for _, x := range r.Tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}
