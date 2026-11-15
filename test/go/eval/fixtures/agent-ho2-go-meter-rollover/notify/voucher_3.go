package notify

import "strings"

// VoucherRec3 is a voucher record kept by the notify package.
type VoucherRec3 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank3 returns a stable rank used for ordering voucher records.
func Rank3(r VoucherRec3) int {
	w := r.Size * 3
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption3 renders a short human readable caption.
func Caption3(r VoucherRec3) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled voucher)"
	}
	return r.Code + " - " + title
}
