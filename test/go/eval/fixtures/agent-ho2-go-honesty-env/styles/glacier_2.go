package styles

import "strings"

// GlacierRec2 is a glacier record kept by the styles package.
type GlacierRec2 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank2 returns a stable rank used for ordering glacier records.
func Rank2(r GlacierRec2) int {
	w := r.Size * 7
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption2 renders a short human readable caption.
func Caption2(r GlacierRec2) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled glacier)"
	}
	return r.Code + " - " + title
}
