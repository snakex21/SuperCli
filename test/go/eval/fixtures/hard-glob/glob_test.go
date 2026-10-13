package glob

import "testing"

func TestMatchBasic(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"main.go", "main.go", true},
		{"*.go", "main.go", true},
		{"*.go", "main.rs", false},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{"[a-c]x", "bx", true},
		{"[a-c]x", "dx", false},
	}
	for _, c := range cases {
		got, err := Match(c.pattern, c.name)
		if err != nil {
			t.Fatalf("Match(%q, %q) unexpected error: %v", c.pattern, c.name, err)
		}
		if got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}
