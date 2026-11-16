package logline

import "testing"

func TestParse(t *testing.T) {
	e, err := Parse("2026-03-01T10:15:00Z warn cache miss ratio above 0.4\n")
	if err != nil {
		t.Fatal(err)
	}
	if e.Level != Warn || e.Msg != "cache miss ratio above 0.4" || e.Time.Minute() != 15 {
		t.Fatalf("got %+v", e)
	}
}

func TestParseErrors(t *testing.T) {
	for _, line := range []string{"", "yesterday INFO x", "2026-03-01T10:15:00Z LOUD x"} {
		if _, err := Parse(line); err == nil {
			t.Errorf("Parse(%q) succeeded", line)
		}
	}
}
