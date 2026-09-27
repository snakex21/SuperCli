package fileops

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func diagnosticQuote(t *testing.T, message string) string {
	t.Helper()
	_, tail, ok := strings.Cut(message, "which reads: ")
	if !ok {
		t.Fatalf("missing near-miss diagnosis: %s", message)
	}
	quoted, err := strconv.QuotedPrefix(tail)
	if err != nil {
		t.Fatal(err)
	}
	text, err := strconv.Unquote(quoted)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestPatchFileDivergenceSnippet(t *testing.T) {
	longPrefix := strings.Repeat("unchanged words ", 60)
	unicodePrefix := "a" + strings.Repeat("ą", 400)
	cases := []struct{ name, content, old, want, absent string }{
		{"long line", "intro\n" + longPrefix + "See [reference](reference.md) for details.\nend\n", longPrefix + "See reference.md for details.", "[reference](reference.md)", "unchanged words unchanged words unchanged words unchanged words unchanged words unchanged words"},
		{"newline", "this is the complete original line\nDO_NOT_SHOW_NEXT_LINE\n", "this is the complete original line plus a wrong suffix", "this is the complete original line", "DO_NOT_SHOW_NEXT_LINE"},
		{"unicode", "intro\n" + unicodePrefix + "ż actual\n", unicodePrefix + "ź wrong", "ż actual", "\\xc4"},
		{"EOF without newline", "this is the complete original line", "this is the complete original line plus a wrong suffix", "this is the complete original line", "DO_NOT_SHOW"},
		{"EOF after newline", "this is the complete original line\n", "this is the complete original line\nmissing tail", "<end of file>", "DO_NOT_SHOW"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, "sample.txt", tc.content)
			_, err := PatchFile(path, []PatchChange{{Old: tc.old, New: "replacement"}}, "")
			if err == nil {
				t.Fatal("wrong anchor must remain rejected")
			}
			quote := diagnosticQuote(t, err.Error())
			if !strings.Contains(quote, tc.want) || strings.Contains(quote, tc.absent) || !utf8.ValidString(quote) || len(quote) > diagSnippet+6 {
				t.Fatalf("mismatch hidden or invalid excerpt: %q; %s", quote, err)
			}
			actual, readErr := os.ReadFile(path)
			if readErr != nil || string(actual) != tc.content {
				t.Fatal("failed patch modified the file")
			}
		})
	}
}

func TestPatchFileDivergenceSavedReplay(t *testing.T) {
	path := os.Getenv("SUPERCLI_PATCH_DIAG_REPLAY")
	if path == "" {
		t.Skip("optional fixed session replay")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Content, Old, Want string }
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	temp := writeTemp(t, "README.md", fixture.Content)
	_, err = PatchFile(temp, []PatchChange{{Old: fixture.Old, New: "replacement"}}, "")
	if err == nil {
		t.Fatal("inexact patch accepted")
	}
	t.Log(err.Error())
	if !strings.Contains(diagnosticQuote(t, err.Error()), fixture.Want) {
		t.Fatal("saved mismatch is not in diagnostic")
	}
	actual, err := os.ReadFile(temp)
	if err != nil || string(actual) != fixture.Content {
		t.Fatal("rejected edit changed content")
	}
}
