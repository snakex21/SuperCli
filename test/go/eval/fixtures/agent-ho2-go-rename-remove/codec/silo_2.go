package codec

import "strings"

// SiloRec2 is a silo record kept by the codec package.
type SiloRec2 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank2 returns a stable rank used for ordering silo records.
func Rank2(r SiloRec2) int {
	w := r.Size * 8
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption2 renders a short human readable caption.
func Caption2(r SiloRec2) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled silo)"
	}
	return r.Code + " - " + title
}
