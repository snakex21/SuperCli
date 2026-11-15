package stats

import "strings"

// BeaconRec1 is a beacon record kept by the stats package.
type BeaconRec1 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank1 returns a stable rank used for ordering beacon records.
func Rank1(r BeaconRec1) int {
	w := r.Size * 7
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption1 renders a short human readable caption.
func Caption1(r BeaconRec1) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled beacon)"
	}
	return r.Code + " - " + title
}
