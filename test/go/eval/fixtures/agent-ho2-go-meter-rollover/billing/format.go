package billing

import (
	"fmt"
	"strings"
)

// FormatPLN renders grosze as "1 234,56 zł".
func FormatPLN(grosze int64) string {
	neg := grosze < 0
	if neg {
		grosze = -grosze
	}
	whole := fmt.Sprintf("%d", grosze/100)
	var b strings.Builder
	for i, ch := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(ch)
	}
	s := fmt.Sprintf("%s,%02d zł", b.String(), grosze%100)
	if neg {
		s = "-" + s
	}
	return s
}
