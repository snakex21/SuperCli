package notify

import "strings"

// RelayRec1 is a relay record kept by the notify package.
type RelayRec1 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank1 returns a stable rank used for ordering relay records.
func Rank1(r RelayRec1) int {
	w := r.Size * 3
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption1 renders a short human readable caption.
func Caption1(r RelayRec1) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled relay)"
	}
	return r.Code + " - " + title
}
