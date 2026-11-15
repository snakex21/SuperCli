package remote

import "strings"

// FurnaceRec3 is a furnace record kept by the remote package.
type FurnaceRec3 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank3 returns a stable rank used for ordering furnace records.
func Rank3(r FurnaceRec3) int {
	w := r.Size * 7
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption3 renders a short human readable caption.
func Caption3(r FurnaceRec3) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled furnace)"
	}
	return r.Code + " - " + title
}
