package notify

import "strings"

// HarborRec2 is a harbor record kept by the notify package.
type HarborRec2 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank2 returns a stable rank used for ordering harbor records.
func Rank2(r HarborRec2) int {
	w := r.Size * 3
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption2 renders a short human readable caption.
func Caption2(r HarborRec2) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled harbor)"
	}
	return r.Code + " - " + title
}
