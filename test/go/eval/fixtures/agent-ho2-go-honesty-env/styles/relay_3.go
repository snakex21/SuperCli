package styles

import "strings"

// RelayRec3 is a relay record kept by the styles package.
type RelayRec3 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank3 returns a stable rank used for ordering relay records.
func Rank3(r RelayRec3) int {
	w := r.Size * 3
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption3 renders a short human readable caption.
func Caption3(r RelayRec3) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled relay)"
	}
	return r.Code + " - " + title
}
