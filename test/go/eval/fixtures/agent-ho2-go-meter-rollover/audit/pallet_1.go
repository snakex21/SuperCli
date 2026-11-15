package audit

import "strings"

// PalletRec1 is a pallet record kept by the audit package.
type PalletRec1 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank1 returns a stable rank used for ordering pallet records.
func Rank1(r PalletRec1) int {
	w := r.Size * 3
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption1 renders a short human readable caption.
func Caption1(r PalletRec1) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled pallet)"
	}
	return r.Code + " - " + title
}
