//go:build evalhidden

package csvline

import (
	"errors"
	"reflect"
	"testing"
)

func TestHiddenParse(t *testing.T) {
	cases := []struct {
		in   string
		want [][]string
	}{
		{"a,b\r\nc,d\r\n", [][]string{{"a", "b"}, {"c", "d"}}},
		{"a,b\r\nc,d", [][]string{{"a", "b"}, {"c", "d"}}},
		{"a\n\nb", [][]string{{"a"}, {""}, {"b"}}},
		{"a\n\n", [][]string{{"a"}, {""}}},
		{"a\r\n\r\nb\r\n", [][]string{{"a"}, {""}, {"b"}}},
		{"a,", [][]string{{"a", ""}}},
		{",", [][]string{{"", ""}}},
		{"a,,b\n", [][]string{{"a", "", "b"}}},
		{" a , b ", [][]string{{" a ", " b "}}},
		{"\"say \"\"hi\"\"\",x", [][]string{{"say \"hi\"", "x"}}},
		{"\"\"", [][]string{{""}}},
		{"\"\",\"\"", [][]string{{"", ""}}},
		{"\"\"\"\"", [][]string{{"\""}}},
		{"\"line1\nline2\",z\nnext", [][]string{{"line1\nline2", "z"}, {"next"}}},
		{"\"crlf\r\ninside\"\r\nq", [][]string{{"crlf\r\ninside"}, {"q"}}},
		{"\"a,b\",\"c\"\"d\"\r\n", [][]string{{"a,b", "c\"d"}}},
		{"x\ry,z", [][]string{{"x\ry", "z"}}},
		{"zażółć,\"gęślą, jaźń\"", [][]string{{"zażółć", "gęślą, jaźń"}}},
		{"a,\"\"\n", [][]string{{"a", ""}}},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHiddenEmptyInput(t *testing.T) {
	got, err := Parse("")
	if err != nil || len(got) != 0 {
		t.Fatalf("Parse(\"\") = %q, %v; want zero records, nil", got, err)
	}
}

func TestHiddenErrors(t *testing.T) {
	cases := []struct {
		in     string
		record int
	}{
		{"\"abc", 1},
		{"\"a\"b,c", 1},
		{"ab\"c", 1},
		{"a\"", 1},
		{"ok\n\"unterminated", 2},
		{"ok\r\nx,\"a\"\"", 2},
		{"a\n\nb\"c", 3},
		{"\"multi\nline\"\nfine\n\"bad\"x", 3},
		{"\"a\" ,b", 1},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if got != nil {
			t.Errorf("Parse(%q) returned records %q alongside an error; want nil", c.in, got)
		}
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Errorf("Parse(%q) error = %v; want *ParseError", c.in, err)
			continue
		}
		if !errors.Is(err, ErrSyntax) {
			t.Errorf("Parse(%q) error %v does not wrap ErrSyntax", c.in, err)
		}
		if pe.Record != c.record {
			t.Errorf("Parse(%q) ParseError.Record = %d, want %d", c.in, pe.Record, c.record)
		}
	}
}
