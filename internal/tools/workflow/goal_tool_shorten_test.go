package workflow

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Audited row #1220: a goal line cut by bytes was stored as invalid UTF-8.
func TestShortenCutsRunesNotBytes(t *testing.T) {
	s := strings.Repeat("Doprowadź survival do pierwszej grywalnej nocy z zombie ", 4)
	for n := 2; n < 80; n++ {
		got := shorten(s, n)
		if !utf8.ValidString(got) || utf8.RuneCountInString(got) > n {
			t.Fatalf("shorten(%d) = %q", n, got)
		}
	}
}
