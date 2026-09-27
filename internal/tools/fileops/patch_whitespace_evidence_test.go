package fileops

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPatchWhitespaceShowsExactFileEvidence(t *testing.T) {
	for _, tc := range []struct{ name, content, old, want string }{
		{"live tab instead of space", "package cache\n\nfunc check() {\n if !ok || now > e.ExpiresAt { return \"\", false }\n}\n", "\tif !ok || now > e.ExpiresAt { return \"\", false }", " if !ok || now > e.ExpiresAt { return \"\", false }"},
		{"collapsed block", "func main() {\n\t\tfmt.Println(\"hi\")\n}\n", "func main() {\n    fmt.Println(\"hi\")\n}", "func main() {\n\t\tfmt.Println(\"hi\")\n}"},
		{"minified", ".hero{display:flex;align-items:center}\n", ".hero { display: flex; align-items: center }", ".hero{display:flex;align-items:center}"},
		{"CRLF", "intro\r\n  value := computeResult()\r\n", "value  := computeResult()", "  value := computeResult()"},
		{"unicode", "intro\n\tżółty := wartość\n", "żółty   := wartość", "\tżółty := wartość"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, "sample.txt", tc.content)
			_, err := PatchFile(path, []PatchChange{{Old: tc.old, New: "replacement"}}, "")
			if err == nil {
				t.Fatal("inexact patch must remain rejected")
			}
			if !strings.Contains(err.Error(), strconv.Quote(tc.want)) {
				t.Fatalf("actual whitespace missing: %s", err)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil || string(after) != tc.content {
				t.Fatal("failed patch wrote to the file")
			}
		})
	}
}

func TestPatchWhitespaceEvidenceIsBoundedAndUnambiguous(t *testing.T) {
	content := strings.Repeat("long same prefix ", 40) + "żółty  := wartość\n"
	old := strings.Replace(content, "żółty  :=", "żółty\t:=", 1)
	hint := whitespaceVerdict(content, old)
	if !strings.Contains(hint, "żółty  := wartość") || len(hint) > 800 || !utf8.ValidString(hint) {
		t.Fatalf("missing mismatch or oversized hint: %s", hint)
	}
	ambiguous := whitespaceVerdict("\tvalue := computeResult()\n    value := computeResult()\n", "value  := computeResult()")
	if !strings.Contains(ambiguous, "matches 2 time(s)") || strings.Contains(ambiguous, "file text at line") || strings.Contains(ambiguous, "file line") {
		t.Fatalf("ambiguous whitespace was represented as a unique repair: %s", ambiguous)
	}
}

func TestWhitespaceSpanMapsNormalizedBytes(t *testing.T) {
	for _, source := range []string{"  alpha\t beta\r\ngamma  ", "żółty\t:= wartość", "a\n\n   b\t c", "plain"} {
		for _, collapse := range []bool{false, true} {
			normalized := stripSpace(source)
			if collapse {
				normalized = squeezeSpace(source)
			}
			for start := 0; start < len(normalized); start++ {
				if isSpaceByte(normalized[start]) {
					continue
				}
				for end := start + 1; end <= len(normalized); end++ {
					if isSpaceByte(normalized[end-1]) {
						continue
					}
					from, to := whitespaceSourceSpan(source, start, end, collapse)
					if from < 0 || to <= from || to > len(source) {
						t.Fatalf("invalid source range %d:%d", from, to)
					}
					got := stripSpace(source[from:to])
					if collapse {
						got = squeezeSpace(source[from:to])
					}
					if got != normalized[start:end] {
						t.Fatalf("mapping %q %d:%d: got %q want %q", source, start, end, got, normalized[start:end])
					}
				}
			}
		}
	}
}

func TestPatchWhitespaceSavedLiveReplay(t *testing.T) {
	path := os.Getenv("SUPERCLI_PATCH_WS_REPLAY")
	if path == "" {
		t.Skip("optional captured coding replay")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Content string
		Args    struct{ Old, New string }
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	file := writeTemp(t, "store.go", saved.Content)
	_, err = PatchFile(file, []PatchChange{{Old: saved.Args.Old, New: saved.Args.New}}, "")
	if err == nil {
		t.Fatal("inexact live anchor was accepted")
	}
	_, tail, ok := strings.Cut(err.Error(), "file text at line 8: ")
	if !ok {
		t.Fatalf("no exact evidence in live replay: %s", err)
	}
	quoted, err := strconv.QuotedPrefix(tail)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := strconv.Unquote(quoted)
	if err != nil {
		t.Fatal(err)
	}
	if actual != " if !ok || now > e.ExpiresAt { return \"\", false }" {
		t.Fatalf("wrong live evidence: %q", actual)
	}
	// Use the already-returned bytes as the exact repair anchor: no reread.
	_, err = PatchFile(file, []PatchChange{{Old: actual, New: strings.Replace(actual, "now > e.", "now >= e.", 1)}}, "")
	if err != nil {
		t.Fatalf("returned anchor cannot repair the live patch: %v", err)
	}
	final, err := os.ReadFile(file)
	if err != nil || string(final) != strings.Replace(saved.Content, "now > e.", "now >= e.", 1) {
		t.Fatal("repair changed unexpected bytes")
	}
}
