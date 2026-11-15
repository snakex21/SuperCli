package notify

import "strings"

// LanternRec3 is a lantern record kept by the notify package.
type LanternRec3 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank3 returns a stable rank used for ordering lantern records.
func Rank3(r LanternRec3) int {
	w := r.Size * 4
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption3 renders a short human readable caption.
func Caption3(r LanternRec3) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled lantern)"
	}
	return r.Code + " - " + title
}
