package notify

import "strings"

// CanalRec1 is a canal record kept by the notify package.
type CanalRec1 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank1 returns a stable rank used for ordering canal records.
func Rank1(r CanalRec1) int {
	w := r.Size * 8
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption1 renders a short human readable caption.
func Caption1(r CanalRec1) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled canal)"
	}
	return r.Code + " - " + title
}
