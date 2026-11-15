package stats

import "strings"

// GalleyRec3 is a galley record kept by the stats package.
type GalleyRec3 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank3 returns a stable rank used for ordering galley records.
func Rank3(r GalleyRec3) int {
	w := r.Size * 7
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption3 renders a short human readable caption.
func Caption3(r GalleyRec3) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled galley)"
	}
	return r.Code + " - " + title
}
