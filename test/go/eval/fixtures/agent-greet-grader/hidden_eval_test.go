package main

import (
	"bytes"
	"testing"
)

func TestHiddenUpperFlag(t *testing.T) {
	cases := map[string][]string{
		"HELLO, BOB!\n":   {"--upper", "--name", "bob", "--excited"},
		"HELLO, WORLD.\n": {"-upper"},
		"Hello, Bob.\n":   {"-name", "Bob"},
	}
	for want, args := range cases {
		var out bytes.Buffer
		if err := run(args, &out); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if out.String() != want {
			t.Fatalf("%v: got %q want %q", args, out.String(), want)
		}
	}
}
