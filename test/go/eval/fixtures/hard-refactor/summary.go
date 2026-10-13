package invoicing

import (
	"fmt"
	"strconv"
	"strings"
)

// Summary renders a one-line human summary.
func Summary(lines []Line) string {
	return fmt.Sprintf("%d positions, total %s PLN", len(lines), moneyStr(total(lines)))
}

func moneyStr(c int64) string {
	s := strconv.FormatFloat(float64(c)/100, 'f', 2, 64)
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String() + "." + frac
}
