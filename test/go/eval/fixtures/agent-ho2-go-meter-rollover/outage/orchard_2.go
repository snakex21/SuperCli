package outage

import "strings"

// OrchardRec2 is a orchard record kept by the outage package.
type OrchardRec2 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank2 returns a stable rank used for ordering orchard records.
func Rank2(r OrchardRec2) int {
	w := r.Size * 9
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption2 renders a short human readable caption.
func Caption2(r OrchardRec2) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled orchard)"
	}
	return r.Code + " - " + title
}
