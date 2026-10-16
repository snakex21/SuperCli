package attachments

import (
	"reflect"
	"testing"
)

func TestSplitTranscriptNames(t *testing.T) {
	cases := []struct {
		in, text string
		names    []string
	}{
		{"co to jest?\n\n\U0001F4CE image.png", "co to jest?", []string{"image.png"}},
		{"porównaj\n\n\U0001F4CE a.png, raport.pdf", "porównaj", []string{"a.png", "raport.pdf"}},
		{"opisz\n\n[files] C.png", "opisz", []string{"C.png"}},
		{"\U0001F4CE only.png", "", []string{"only.png"}},
		{"zwykły tekst", "zwykły tekst", nil},
		// A marker in the user's own text, not in a final single-line paragraph.
		{"\U0001F4CE w środku\ndalej tekst", "\U0001F4CE w środku\ndalej tekst", nil},
		{"a\n\n\U0001F4CE x.png\nreszta", "a\n\n\U0001F4CE x.png\nreszta", nil},
	}
	for _, c := range cases {
		text, names := SplitTranscriptNames(c.in)
		if text != c.text || !reflect.DeepEqual(names, c.names) {
			t.Errorf("SplitTranscriptNames(%q) = %q %v, want %q %v", c.in, text, names, c.text, c.names)
		}
	}
}
