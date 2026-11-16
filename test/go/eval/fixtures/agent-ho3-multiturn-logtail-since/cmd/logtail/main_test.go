package main

import (
	"bytes"
	"strings"
	"testing"
)

const sample = `2026-03-01T10:00:00Z INFO started
2026-03-01T10:05:00Z DEBUG warmup done
2026-03-01T10:10:00Z WARN slow request /api/orders
`

func TestLevelFilter(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"--level", "info"}, strings.NewReader(sample), &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	want := "2026-03-01T10:00:00Z INFO started\n2026-03-01T10:10:00Z WARN slow request /api/orders\n"
	if out.String() != want {
		t.Fatalf("got %q", out.String())
	}
}

func TestBadLevel(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--level", "loud"}, strings.NewReader(sample), &out, &errb); code != 2 {
		t.Fatalf("exit %d", code)
	}
}
