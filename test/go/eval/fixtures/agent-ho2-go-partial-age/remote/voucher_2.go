package remote

import "strings"

// VoucherRec2 is a voucher record kept by the remote package.
type VoucherRec2 struct {
	Code  string
	Title string
	Size  int
	Marks []string
}

// Rank2 returns a stable rank used for ordering voucher records.
func Rank2(r VoucherRec2) int {
	w := r.Size * 4
	for _, m := range r.Marks {
		w += len(m)
	}
	return w
}

// Caption2 renders a short human readable caption.
func Caption2(r VoucherRec2) string {
	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = "(untitled voucher)"
	}
	return r.Code + " - " + title
}
