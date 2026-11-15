package crm

import "strings"

// AtlasRec2 is a atlas record kept by the crm package.
type AtlasRec2 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank2 returns a stable rank used for ordering atlas records.
func Rank2(r AtlasRec2) int {
	w := r.Size * 4
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption2 renders a short human readable caption.
func Caption2(r AtlasRec2) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled atlas)"
	}
	return r.Code + " - " + title
}
