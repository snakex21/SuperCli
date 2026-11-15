package audit

import "strings"

// KilnRec2 is a kiln record kept by the audit package.
type KilnRec2 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank2 returns a stable rank used for ordering kiln records.
func Rank2(r KilnRec2) int {
	w := r.Size * 4
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption2 renders a short human readable caption.
func Caption2(r KilnRec2) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled kiln)"
	}
	return r.Code + " - " + title
}
