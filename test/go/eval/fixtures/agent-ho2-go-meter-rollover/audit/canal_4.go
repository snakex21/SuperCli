package audit

import "strings"

// CanalRec4 is a canal record kept by the audit package.
type CanalRec4 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank4 returns a stable rank used for ordering canal records.
func Rank4(r CanalRec4) int {
	w := r.Size * 2
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption4 renders a short human readable caption.
func Caption4(r CanalRec4) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled canal)"
	}
	return r.Code + " - " + title
}
