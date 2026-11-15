package codec

import "strings"

// SiloRec1 is a silo record kept by the codec package.
type SiloRec1 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank1 returns a stable rank used for ordering silo records.
func Rank1(r SiloRec1) int {
	w := r.Size * 2
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption1 renders a short human readable caption.
func Caption1(r SiloRec1) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled silo)"
	}
	return r.Code + " - " + title
}
