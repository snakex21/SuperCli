package memory

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMarkdownPositionReaderMatchesFullParser(t *testing.T) {
	header := "## a (2026-10-09T10:00:00Z, user)"
	for _, fixture := range []struct{ name, body string }{
		{"empty", ""}, {"no-header", "preamble\nonly text\n"},
		{"single", header + "\nbody\n"},
		{"no-final-newline", header + "\nbody"},
		{"blank-content", header + "\n\n\n"},
		{"crlf", "# memory\r\n\r\n" + header + "\r\nα\r\n\t\r\n\r\n## b (bad time, tool)\r\nβ\r\n\r\n"},
		{"invalid-header", header + "\n## not a header\n## bad#id (now, user)\ntext"},
		{"embedded-header", header + "\nbody\n## nested (2026-10-09T10:00:00Z, agent)\nlast\n"},
		{"large-content", header + "\n" + strings.Repeat("file.go: project context\n", 600)},
		{"duplicate-id", header + "\nfirst\n" + header + "\nsecond"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "general.md")
			if err := os.WriteFile(path, []byte(fixture.body), 0600); err != nil {
				t.Fatal(err)
			}
			full, err := mdRead(path)
			if err != nil {
				t.Fatal(err)
			}
			var want []markdownPosition
			for _, e := range full {
				want = append(want, markdownPosition{ID: e.ID, LineStart: e.LineStart, LineEnd: e.LineEnd})
			}
			got, err := mdReadPositions(path)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("positions %+v want %+v: %v", got, want, err)
			}
		})
	}
}

func TestMarkdownPositionReaderPreservesReadFailures(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{filepath.Join(dir, "missing.md"), dir, filepath.Join(dir, "oversized.md")} {
		if filepath.Ext(path) == ".md" && strings.Contains(path, "oversized") {
			if err := os.WriteFile(path, []byte("## a (now, user)\n"+strings.Repeat("x", 70000)), 0600); err != nil {
				t.Fatal(err)
			}
		}
		full, oldErr := mdRead(path)
		got, err := mdReadPositions(path)
		if (oldErr == nil) != (err == nil) {
			t.Fatalf("different read failure for %s: %v/%v", path, oldErr, err)
		}
		if err != nil {
			if err.Error() != oldErr.Error() || got != nil || full != nil {
				t.Fatalf("read failure changed: %v/%v", oldErr, err)
			}
		} else if len(got) != len(full) {
			t.Fatal("missing path changed")
		}
	}
}
