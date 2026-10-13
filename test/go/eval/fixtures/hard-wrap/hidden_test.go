//go:build evalhidden

package textwrap

import (
	"errors"
	"reflect"
	"testing"
	"unicode/utf8"
)

func TestHiddenWrap(t *testing.T) {
	cases := []struct {
		text  string
		width int
		want  []string
	}{
		{"ab cd", 5, []string{"ab cd"}},
		{"ab cd", 4, []string{"ab", "cd"}},
		{"  lots   of\tspace  ", 20, []string{"lots of space"}},
		{"a\rb", 10, []string{"a b"}},
		{"one\ntwo three", 20, []string{"one", "two three"}},
		{"one\r\ntwo", 20, []string{"one", "two"}},
		{"a\n\nb", 5, []string{"a", "", "b"}},
		{"a\n", 5, []string{"a", ""}},
		{"\n", 5, []string{"", ""}},
		{"   ", 5, []string{""}},
		{"x \n y", 5, []string{"x", "y"}},
		{"abcdefgh ij", 3, []string{"abc", "def", "gh", "ij"}},
		{"abcdef ij", 3, []string{"abc", "def", "ij"}},
		{"ab cdefgh i", 3, []string{"ab", "cde", "fgh", "i"}},
		{"abcde f", 4, []string{"abcd", "e f"}},
		{"ąęść żółw", 4, []string{"ąęść", "żółw"}},
		{"źdźbło trawy", 3, []string{"źdź", "bło", "tra", "wy"}},
		{"Łódź", 1, []string{"Ł", "ó", "d", "ź"}},
		{"日本語のテキスト", 3, []string{"日本語", "のテキ", "スト"}},
		{"Zażółć gęślą jaźń. Pchnąć w tę łódź jeża", 15, []string{"Zażółć gęślą", "jaźń. Pchnąć w", "tę łódź jeża"}},
	}
	for _, c := range cases {
		got, err := Wrap(c.text, c.width)
		if err != nil {
			t.Errorf("Wrap(%q, %d) unexpected error: %v", c.text, c.width, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Wrap(%q, %d) = %q, want %q", c.text, c.width, got, c.want)
		}
		for _, line := range got {
			if !utf8.ValidString(line) {
				t.Errorf("Wrap(%q, %d) produced invalid UTF-8 line %q", c.text, c.width, line)
			}
			if utf8.RuneCountInString(line) > c.width {
				t.Errorf("Wrap(%q, %d) produced over-long line %q", c.text, c.width, line)
			}
		}
	}
}

func TestHiddenEmpty(t *testing.T) {
	got, err := Wrap("", 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("Wrap(\"\", 10) = %q, %v; want no lines, nil", got, err)
	}
}

func TestHiddenBadWidth(t *testing.T) {
	for _, w := range []int{0, -1} {
		got, err := Wrap("abc", w)
		if !errors.Is(err, ErrWidth) || got != nil {
			t.Errorf("Wrap(\"abc\", %d) = %q, %v; want nil, ErrWidth", w, got, err)
		}
	}
}
