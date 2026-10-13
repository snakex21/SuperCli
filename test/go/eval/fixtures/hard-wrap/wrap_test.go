package textwrap

import (
	"reflect"
	"testing"
)

func TestWrapBasic(t *testing.T) {
	cases := []struct {
		text  string
		width int
		want  []string
	}{
		{"the quick brown fox", 10, []string{"the quick", "brown fox"}},
		{"zażółć gęślą jaźń", 12, []string{"zażółć gęślą", "jaźń"}},
		{"Wółczyńskiego", 5, []string{"Wółcz", "yński", "ego"}},
	}
	for _, c := range cases {
		got, err := Wrap(c.text, c.width)
		if err != nil {
			t.Fatalf("Wrap(%q, %d) unexpected error: %v", c.text, c.width, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Wrap(%q, %d) = %q, want %q", c.text, c.width, got, c.want)
		}
	}
}
