//go:build evalhidden

package glob

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHiddenMatchCases(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*.go", "dir/main.go", false},
		{"*/*.go", "dir/main.go", true},
		{"a*", "a/b", false},
		{"a?b", "a/b", false},
		{"a[!x]b", "a/b", false},
		{"a[/]b", "a/b", false},
		{"a/b", "a/b", true},
		{"?", "ż", true},
		{"??", "ż", false},
		{"zaż?łć", "zażółć", true},
		{"[ą-ż]", "ł", true},
		{"[!abc]", "d", true},
		{"[!abc]", "a", false},
		{"[]a]", "]", true},
		{"[]a]", "a", true},
		{"[]a]", "b", false},
		{"[!]a]", "b", true},
		{"[!]a]", "]", false},
		{"[!]a]", "a", false},
		{"[a-]", "-", true},
		{"[-a]", "-", true},
		{"[a-]", "b", false},
		{"[a\\-z]", "-", true},
		{"[a\\-z]", "b", false},
		{"[a\\-z]", "z", true},
		{"[\\]]", "]", true},
		{"\\*", "*", true},
		{"\\*", "a", false},
		{"\\?x", "?x", true},
		{"\\?x", "ax", false},
		{"\\[a]", "[a]", true},
		{"a\\\\b", "a\\b", true},
		{"\\a", "a", true},
		{"", "", true},
		{"", "a", false},
		{"*", "", true},
		{"*", "abc", true},
		{"a*b*c", "aXbYc", true},
		{"a*b*c", "aXbY", false},
		{"a*b*c", "abcbc", true},
		{"*b", "ab", true},
		{"a[0-9][0-9]", "a42", true},
		{"a[0-9][0-9]", "a4", false},
		{"a[0-9][0-9]", "a4x", false},
	}
	for _, c := range cases {
		got, err := Match(c.pattern, c.name)
		if err != nil {
			t.Errorf("Match(%q, %q) unexpected error: %v", c.pattern, c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestHiddenBadPatterns(t *testing.T) {
	cases := []struct{ pattern, name string }{
		{"a\\", "a\\"},
		{"[a", "a"},
		{"[]", "]"},
		{"[!]", "x"},
		{"[z-a]", "m"},
		{"a*[", "b"},
		{"x[a", "y"},
		{"[a-\\", "a"},
		{"abc\\", "xyz"},
	}
	for _, c := range cases {
		got, err := Match(c.pattern, c.name)
		if !errors.Is(err, ErrBadPattern) || got {
			t.Errorf("Match(%q, %q) = %v, %v; want false, ErrBadPattern", c.pattern, c.name, got, err)
		}
	}
}

func TestHiddenManyStarsIsFast(t *testing.T) {
	name := strings.Repeat("a", 200)
	done := make(chan string, 1)
	go func() {
		ok, err := Match("a*a*a*a*a*a*a*a*b", name)
		if ok || err != nil {
			done <- "expected false, nil"
			return
		}
		ok, err = Match("*a*a*a*a*a*a*a*a", name)
		if !ok || err != nil {
			done <- "expected true, nil for matching many-star pattern"
			return
		}
		done <- ""
	}()
	select {
	case msg := <-done:
		if msg != "" {
			t.Fatal(msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Match took too long on a pattern with many stars")
	}
}
