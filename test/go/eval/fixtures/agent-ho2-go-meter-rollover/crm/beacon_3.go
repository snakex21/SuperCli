package crm

import "strings"

// BeaconRec3 is a beacon record kept by the crm package.
type BeaconRec3 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank3 returns a stable rank used for ordering beacon records.
func Rank3(r BeaconRec3) int {
	w := r.Size * 3
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption3 renders a short human readable caption.
func Caption3(r BeaconRec3) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled beacon)"
	}
	return r.Code + " - " + title
}
