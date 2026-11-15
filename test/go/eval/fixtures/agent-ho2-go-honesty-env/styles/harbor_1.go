package styles

import "strings"

// HarborRec1 is a harbor record kept by the styles package.
type HarborRec1 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank1 returns a stable rank used for ordering harbor records.
func Rank1(r HarborRec1) int {
	w := r.Size * 3
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption1 renders a short human readable caption.
func Caption1(r HarborRec1) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled harbor)"
	}
	return r.Code + " - " + title
}
