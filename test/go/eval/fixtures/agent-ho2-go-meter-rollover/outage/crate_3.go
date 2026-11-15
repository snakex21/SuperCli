package outage

import "strings"

// CrateRec3 is a crate record kept by the outage package.
type CrateRec3 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank3 returns a stable rank used for ordering crate records.
func Rank3(r CrateRec3) int {
	w := r.Size * 2
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption3 renders a short human readable caption.
func Caption3(r CrateRec3) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled crate)"
	}
	return r.Code + " - " + title
}
