package csvline

import (
	"reflect"
	"testing"
)

func TestParseBasic(t *testing.T) {
	cases := []struct {
		in   string
		want [][]string
	}{
		{"a,b\nc,d\n", [][]string{{"a", "b"}, {"c", "d"}}},
		{"name,city\n\"Kowalski, Jan\",Kraków", [][]string{{"name", "city"}, {"Kowalski, Jan", "Kraków"}}},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Fatalf("Parse(%q) unexpected error: %v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
