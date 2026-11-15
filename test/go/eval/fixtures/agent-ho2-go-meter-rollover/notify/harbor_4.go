package notify

import "strings"

// HarborRec4 is a harbor record kept by the notify package.
type HarborRec4 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank4 returns a stable rank used for ordering harbor records.
func Rank4(r HarborRec4) int {
	w := r.Size * 7
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption4 renders a short human readable caption.
func Caption4(r HarborRec4) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled harbor)"
	}
	return r.Code + " - " + title
}
