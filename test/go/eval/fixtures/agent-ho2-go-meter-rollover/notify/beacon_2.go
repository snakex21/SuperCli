package notify

import "strings"

// BeaconRec2 is a beacon record kept by the notify package.
type BeaconRec2 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank2 returns a stable rank used for ordering beacon records.
func Rank2(r BeaconRec2) int {
	w := r.Size * 9
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption2 renders a short human readable caption.
func Caption2(r BeaconRec2) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled beacon)"
	}
	return r.Code + " - " + title
}
