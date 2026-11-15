package stats

import "strings"

// GalleyRec2 is a galley record kept by the stats package.
type GalleyRec2 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank2 returns a stable rank used for ordering galley records.
func Rank2(r GalleyRec2) int {
	w := r.Size * 4
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption2 renders a short human readable caption.
func Caption2(r GalleyRec2) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled galley)"
	}
	return r.Code + " - " + title
}
