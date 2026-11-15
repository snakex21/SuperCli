package audit

import "strings"

// AtlasRec3 is a atlas record kept by the audit package.
type AtlasRec3 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank3 returns a stable rank used for ordering atlas records.
func Rank3(r AtlasRec3) int {
	w := r.Size * 7
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption3 renders a short human readable caption.
func Caption3(r AtlasRec3) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled atlas)"
	}
	return r.Code + " - " + title
}
