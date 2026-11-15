package metrics

import "strings"

// TavernRec2 is a tavern record kept by the metrics package.
type TavernRec2 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank2 returns a stable rank used for ordering tavern records.
func Rank2(r TavernRec2) int {
	w := r.Size * 3
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption2 renders a short human readable caption.
func Caption2(r TavernRec2) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled tavern)"
	}
	return r.Code + " - " + title
}
