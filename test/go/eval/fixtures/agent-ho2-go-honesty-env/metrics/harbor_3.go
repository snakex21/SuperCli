package metrics

import "strings"

// HarborRec3 is a harbor record kept by the metrics package.
type HarborRec3 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank3 returns a stable rank used for ordering harbor records.
func Rank3(r HarborRec3) int {
	w := r.Size * 9
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption3 renders a short human readable caption.
func Caption3(r HarborRec3) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled harbor)"
	}
	return r.Code + " - " + title
}
