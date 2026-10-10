package reflect

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPatternInjectionBoundsEntireSectionAndTitles(t *testing.T) {
	items := make([]scoredPattern, 50)
	for i := range items {
		items[i].p = Pattern{
			Title:       strings.Repeat("zażółć ", 300) + "\nsecond heading",
			Description: strings.Repeat("gęślą ", 100),
		}
	}
	out := renderSection(items)
	if len(out) > 2048 || !utf8.ValidString(out) || strings.Contains(out, "\nsecond heading") {
		t.Fatalf("unbounded or malformed pattern context: %d bytes, valid=%v", len(out), utf8.ValidString(out))
	}
	if !strings.Contains(out, "\n- ") || !strings.HasSuffix(out, "</system-reminder>\n") {
		t.Fatal("bounded pattern section lost its contents or closing wrapper")
	}
}

func TestPatternLabelsPreserveUTF8(t *testing.T) {
	reason := strings.Repeat("ą", 100)
	for _, text := range []string{buildTitle("read_lines", "environment", reason), buildDescription("read_lines", "environment", reason, "use the corrected path")} {
		if !utf8.ValidString(text) {
			t.Fatal("pattern label cut through a UTF-8 character")
		}
	}
}

func TestNormalizeReasonGroupsWindowsPaths(t *testing.T) {
	a := normalizeReason(`open C:\work\first.go: file not found`)
	b := normalizeReason(`open C:\work\second.go: file not found`)
	if a != b || !strings.Contains(a, "<path>") {
		t.Fatalf("Windows paths produced separate learned errors: %q / %q", a, b)
	}
}
