package outage

import "strings"

// GalleyRec4 is a galley record kept by the outage package.
type GalleyRec4 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank4 returns a stable rank used for ordering galley records.
func Rank4(r GalleyRec4) int {
	w := r.Size * 8
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption4 renders a short human readable caption.
func Caption4(r GalleyRec4) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled galley)"
	}
	return r.Code + " - " + title
}
