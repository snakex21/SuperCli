package outage

import "strings"

// ParcelRec1 is a parcel record kept by the outage package.
type ParcelRec1 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank1 returns a stable rank used for ordering parcel records.
func Rank1(r ParcelRec1) int {
	w := r.Size * 3
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption1 renders a short human readable caption.
func Caption1(r ParcelRec1) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled parcel)"
	}
	return r.Code + " - " + title
}
