package main

import (
	"bytes"
	"testing"
)

func TestDefaultGreeting(t *testing.T) {
	var out bytes.Buffer
	if err := run(nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "Hello, world.\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestExcitedName(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"-name", "Ada", "-excited"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "Hello, Ada!\n" {
		t.Fatalf("got %q", out.String())
	}
}
