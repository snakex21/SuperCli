package remote

import "strings"

// AtlasRec1 is a atlas record kept by the remote package.
type AtlasRec1 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank1 returns a stable rank used for ordering atlas records.
func Rank1(r AtlasRec1) int {
	w := r.Size * 2
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption1 renders a short human readable caption.
func Caption1(r AtlasRec1) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled atlas)"
	}
	return r.Code + " - " + title
}
