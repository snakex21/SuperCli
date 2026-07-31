package fileops

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// These tests reproduce the four real patch_file failures found in the
// tool-call forensics (1095 calls). In every one of them the model had the
// right content and got back a message that told it nothing, so it regenerated
// the whole patch. Each test asserts the failure now carries the information
// needed to correct the call.

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Case 1 — a 7-change patch where change 6 misses. The model must learn that
// changes 0-5 were fine so it can resend only the one that failed.
func TestPatchFile_ReportsWhichChangesMatched(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 7; i++ {
		fmt.Fprintf(&sb, "const value%d = %d;\n", i, i)
	}
	path := writeTemp(t, "script.js", sb.String())

	changes := make([]PatchChange, 0, 7)
	for i := 0; i < 6; i++ {
		changes = append(changes, PatchChange{
			Old: fmt.Sprintf("const value%d = %d;", i, i),
			New: fmt.Sprintf("const value%d = %d;", i, i*10),
		})
	}
	// The seventh change quotes content that is not in the file.
	changes = append(changes, PatchChange{Old: "const value6 = 999;", New: "const value6 = 60;"})

	_, err := PatchFile(path, changes, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "changes 0-5 matched, change 6 did not") {
		t.Fatalf("message does not say which changes matched: %s", msg)
	}
	if !strings.Contains(msg, "resend change 6 alone") {
		t.Fatalf("message does not tell the model what to resend: %s", msg)
	}
	// Atomicity is unchanged: nothing was written.
	got, _ := os.ReadFile(path)
	if string(got) != sb.String() {
		t.Fatal("file was modified by a failing patch")
	}
}

// Case 2 — seq 46 (index.html) and seq 52 (script.js): minified source where
// `old` differs from the file ONLY in whitespace, and the whitespace-insensitive
// match is unique.
func TestPatchFile_ReportsWhitespaceOnlyDifference(t *testing.T) {
	path := writeTemp(t, "styles.css", ".hero{display:flex;align-items:center}\n")

	// What the model sent: same bytes, humane spacing.
	old := ".hero { display: flex; align-items: center }"
	_, err := PatchFile(path, []PatchChange{{Old: old, New: ".hero{display:grid}"}}, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "whitespace") {
		t.Fatalf("message does not mention whitespace: %s", msg)
	}
	if !strings.Contains(msg, "matches 1 time(s)") {
		t.Fatalf("message does not report a unique normalised match: %s", msg)
	}
}

// The other whitespace shape: the text is identical but indentation and line
// breaks were re-flowed, so collapsing whitespace runs is enough to match.
func TestPatchFile_ReportsCollapsedWhitespaceMatch(t *testing.T) {
	path := writeTemp(t, "app.go", "func main() {\n\t\tfmt.Println(\"hi\")\n}\n")

	old := "func main() {\n    fmt.Println(\"hi\")\n}"
	_, err := PatchFile(path, []PatchChange{{Old: old, New: "func main() {}"}}, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "whitespace runs are collapsed") {
		t.Fatalf("message does not report the collapsed match: %s", msg)
	}
	if !strings.Contains(msg, "indentation or line breaks differ") {
		t.Fatalf("message does not name the cause: %s", msg)
	}
}

var prefixReport = regexp.MustCompile(`first (\d+) of (\d+) chars of old match at line (\d+)`)

// Case 3 — seq 76 (script.js) and seq 46 (styles.css): ~95% of `old` matches and
// the two diverge only in the tail. The message must report the prefix as the
// near miss it is, point at the divergence, and show the line that is actually
// in the file.
func TestPatchFile_ReportsLongestPrefixAndDivergence(t *testing.T) {
	var file strings.Builder
	for i := 1; i <= 80; i++ {
		if i == 78 {
			file.WriteString("  const total = subtotal + shipping;\n")
			continue
		}
		fmt.Fprintf(&file, "  const line%02d = %d;\n", i, i)
	}
	path := writeTemp(t, "script.js", file.String())

	// The model reproduced lines 41..80 but mis-remembered line 78.
	var old strings.Builder
	for i := 41; i <= 80; i++ {
		if i == 78 {
			old.WriteString("  const total = subtotal + shipping + tax;\n")
			continue
		}
		fmt.Fprintf(&old, "  const line%02d = %d;\n", i, i)
	}

	_, err := PatchFile(path, []PatchChange{{Old: old.String(), New: "// replaced\n"}}, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	m := prefixReport.FindStringSubmatch(msg)
	if m == nil {
		t.Fatalf("message does not locate the matching prefix: %s", msg)
	}
	matched, _ := strconv.Atoi(m[1])
	total, _ := strconv.Atoi(m[2])
	// The forensic cases sat at 95-96%; anything in that band proves the model
	// is told it had the block and only drifted in the tail.
	if matched*100 < total*90 {
		t.Fatalf("prefix reported as %d of %d chars, expected a near miss: %s", matched, total, msg)
	}
	if m[3] != "41" {
		t.Fatalf("prefix located at line %s, want 41: %s", m[3], msg)
	}
	if !strings.Contains(msg, "file line 78") {
		t.Fatalf("message does not locate the divergence: %s", msg)
	}
	if !strings.Contains(msg, "const total = subtotal + shipping;") {
		t.Fatalf("message does not show the real line: %s", msg)
	}
}

// A short accidental prefix ("const ") is noise, not a near miss, and must not
// be reported as one.
func TestPatchFile_SuppressesNoisyPrefix(t *testing.T) {
	path := writeTemp(t, "a.txt", "const alpha = 1;\n")
	_, err := PatchFile(path, []PatchChange{{Old: "const zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz = 2;", New: "x"}}, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	if strings.Contains(err.Error(), "chars of old match") {
		t.Fatalf("noisy prefix reported: %s", err.Error())
	}
}

// Case 4 — a one-line file (minified bundle). Every patch there has to
// reproduce a byte run inside that single line; say so.
func TestPatchFile_ReportsSingleLineFile(t *testing.T) {
	path := writeTemp(t, "bundle.min.js", `!function(){var a=1;var b=2;console.log(a+b)}();`)

	_, err := PatchFile(path, []PatchChange{{Old: "var c = 3;", New: "var c=4;"}}, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "file is a single line") {
		t.Fatalf("message does not report the single-line file: %s", msg)
	}
}

// --- wrong-file diagnostics ------------------------------------------------
//
// Case 5 — session 951feaa30759ab0f (2026-07-25). The model called patch_file
// with path=scripts.js (4804 B) and three changes, two of which were HTML
// blocks belonging to index.html. Sum of `old` was 5432 B, 113% of the target
// file; all three changes matched 0 times. Every existing branch went silent:
// the HTML does not appear in the JS under any whitespace normalisation, the
// longest matching prefix was 4 chars (indentation) against len(old)=453 so the
// near-miss threshold rejected it, and the file is multi-line. The model got a
// bare "found 0" and regenerated the patch: 3948 tokens, 32.7% of the session.

// htmlBlock builds a chunk of index.html markup of roughly n bytes.
func htmlBlock(id string, n int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "        <section class=%q>\n", id)
	for i := 0; sb.Len() < n-60; i++ {
		fmt.Fprintf(&sb, "          <div class=\"row\" data-idx=\"%d\">\n"+
			"            <span class=\"label\">Field %d</span>\n"+
			"            <input type=\"text\" name=\"f%d\" />\n"+
			"          </div>\n", i, i, i)
	}
	sb.WriteString("        </section>")
	return sb.String()
}

// jsSource builds a plausible scripts.js of roughly n bytes.
func jsSource(n int) string {
	var sb strings.Builder
	sb.WriteString("(function () {\n  \"use strict\";\n")
	for i := 0; sb.Len() < n-40; i++ {
		fmt.Fprintf(&sb, "  function handler%02d(event) {\n"+
			"    return Number(event.target.value) * %d;\n  }\n", i, i)
	}
	sb.WriteString("})();\n")
	return sb.String()
}

func TestPatchFile_ReportsPatchAimedAtWrongFile(t *testing.T) {
	content := jsSource(4804)
	path := writeTemp(t, "scripts.js", content)

	changes := []PatchChange{
		{Old: htmlBlock("cart", 2500), New: "<section class=\"cart\"></section>"},
		{Old: htmlBlock("summary", 2500), New: "<section class=\"summary\"></section>"},
		{Old: "  <div id=\"checkout-total\" class=\"total\"></div>", New: "  <div id=\"checkout-total\"></div>"},
	}
	total := 0
	for _, ch := range changes {
		total += len(ch.Old)
	}
	// The setup must keep the production shape, or the test proves nothing.
	if total <= len(content) {
		t.Fatalf("setup: old totals %d B, file is %d B; the real case had old > file", total, len(content))
	}

	_, err := PatchFile(path, changes, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "none of the 3 changes match this file at all") {
		t.Fatalf("message does not say the whole patch missed: %s", msg)
	}
	if !strings.Contains(msg, "the path is probably wrong") {
		t.Fatalf("message does not name the path as the suspect: %s", msg)
	}
	if !strings.Contains(msg, "bytes of old but the file is only") {
		t.Fatalf("message does not report the impossible byte total: %s", msg)
	}
	// It must not simultaneously claim the block nearly matched.
	if strings.Contains(msg, "chars of old match at line") {
		t.Fatalf("wrong-file failure also reported as a near miss: %s", msg)
	}
	if got, _ := os.ReadFile(path); string(got) != content {
		t.Fatal("file was modified by a failing patch")
	}
}

// A single `old` larger than the whole file cannot possibly match; that is
// arithmetic, available before any search.
func TestPatchFile_ReportsOldLargerThanFile(t *testing.T) {
	content := "let a = 1;\nlet b = 2;\n"
	path := writeTemp(t, "tiny.js", content)

	_, err := PatchFile(path, []PatchChange{{Old: htmlBlock("cart", 600), New: "x"}}, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "but the file is only 22 bytes") {
		t.Fatalf("message does not compare old against the file size: %s", msg)
	}
}

// THE DISCRIMINATING TEST. Same shape as the wrong-file case — several changes,
// none of them matching, `old` well over the foreign-block threshold — except
// the blocks really do come from this file and only drifted. The model must be
// told where it diverged, and must NOT be sent to check the path.
func TestPatchFile_DriftIsNotReportedAsWrongFile(t *testing.T) {
	var file strings.Builder
	for i := 1; i <= 120; i++ {
		fmt.Fprintf(&file, "  const line%03d = compute(%d, base);\n", i, i)
	}
	content := file.String()
	path := writeTemp(t, "app.js", content)

	// Two blocks copied out of the file, each with one wrong digit in the tail.
	blockA := content[strings.Index(content, "  const line010"):strings.Index(content, "  const line030")]
	blockB := content[strings.Index(content, "  const line060"):strings.Index(content, "  const line080")]
	changes := []PatchChange{
		{Old: strings.Replace(blockA, "compute(29, base)", "compute(29, bases)", 1), New: "// A\n"},
		{Old: strings.Replace(blockB, "compute(79, base)", "compute(79, bases)", 1), New: "// B\n"},
	}
	for _, ch := range changes {
		if len(ch.Old) < diagForeignMinOld {
			t.Fatalf("setup: old is %d B, below the foreign threshold %d", len(ch.Old), diagForeignMinOld)
		}
		if strings.Count(content, ch.Old) != 0 {
			t.Fatal("setup: change was supposed to miss")
		}
	}

	_, err := PatchFile(path, changes, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "chars of old match at line") {
		t.Fatalf("drift not reported as a near miss: %s", msg)
	}
	if strings.Contains(msg, "the path is probably wrong") ||
		strings.Contains(msg, "shares almost nothing") {
		t.Fatalf("plain drift reported as the wrong file: %s", msg)
	}
}

// A block that does come from this file but whose FIRST line is wrong has a
// tiny prefix too. The interior probes catch that and keep the wrong-file
// verdict quiet: a false "check the path" would send the model to rewrite a
// correct call.
func TestPatchFile_EarlyTypoIsNotReportedAsWrongFile(t *testing.T) {
	var file strings.Builder
	for i := 1; i <= 120; i++ {
		fmt.Fprintf(&file, "  const line%03d = compute(%d, base);\n", i, i)
	}
	content := file.String()
	path := writeTemp(t, "early.js", content)

	block := content[strings.Index(content, "  const line010"):strings.Index(content, "  const line030")]
	old := strings.Replace(block, "  const line010 = compute(10, base);", "  const line010 = compute(10, BASE);", 1)

	_, err := PatchFile(path, []PatchChange{{Old: old, New: "// x\n"}}, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	if msg := err.Error(); strings.Contains(msg, "shares almost nothing") ||
		strings.Contains(msg, "the path is probably wrong") {
		t.Fatalf("a block from this file was reported as foreign: %s", msg)
	}
}

// Regression: when some changes matched and one did not, the failure stays a
// per-change drift report. The wrong-file verdict must not appear — changes
// 0-5 landing proves the path is right.
func TestPatchFile_PartialMatchNeverBlamesThePath(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 7; i++ {
		fmt.Fprintf(&sb, "const value%d = %d;\n", i, i)
	}
	path := writeTemp(t, "partial.js", sb.String())

	changes := make([]PatchChange, 0, 7)
	for i := 0; i < 6; i++ {
		changes = append(changes, PatchChange{
			Old: fmt.Sprintf("const value%d = %d;", i, i),
			New: fmt.Sprintf("const value%d = %d;", i, i*10),
		})
	}
	changes = append(changes, PatchChange{Old: htmlBlock("footer", 400), New: "x"})

	_, err := PatchFile(path, changes, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "changes 0-5 matched, change 6 did not") {
		t.Fatalf("lost the per-change report: %s", msg)
	}
	if strings.Contains(msg, "none of the") || strings.Contains(msg, "the path is probably wrong") {
		t.Fatalf("blamed the path although changes 0-5 matched: %s", msg)
	}
}

// Too many occurrences is the mirror failure and gets the count it needs.
func TestPatchFile_ReportsAmbiguousOld(t *testing.T) {
	path := writeTemp(t, "dup.txt", "x = 1;\nx = 1;\nx = 1;\n")
	_, err := PatchFile(path, []PatchChange{{Old: "x = 1;", New: "x = 2;"}}, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "old occurs 3 times") || !strings.Contains(msg, "expected_count=3") {
		t.Fatalf("message does not explain the ambiguity: %s", msg)
	}
}

// "add surrounding context" is advice until the model knows WHERE the
// duplicates are; the line numbers make it an instruction.
func TestPatchFile_AmbiguousOldCarriesLineNumbers(t *testing.T) {
	path := writeTemp(t, "dup2.txt", "head\nx = 1;\nmid\nx = 1;\ntail\n")
	_, err := PatchFile(path, []PatchChange{{Old: "x = 1;", New: "x = 2;"}}, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	if msg := err.Error(); !strings.Contains(msg, "at lines 2,4") {
		t.Fatalf("no occurrence lines in: %s", msg)
	}
}

// A boilerplate string must not paste hundreds of numbers into the error.
func TestOccurrenceLines_Capped(t *testing.T) {
	content := strings.Repeat("dup\n", 50)
	got := occurrenceLines(content, "dup")
	if strings.Count(got, ",") != diagMaxOccLines || !strings.HasSuffix(got, "...") {
		t.Fatalf("uncapped or malformed list: %q", got)
	}
}

// The one piece of state the tool really holds: it patched this file already,
// so the model is matching against bytes older than its own edit.
func TestPatchFile_ReportsItsOwnEarlierWrite(t *testing.T) {
	path := writeTemp(t, "twice.txt", "alpha\nbeta\n")
	if _, err := PatchFile(path, []PatchChange{{Old: "alpha", New: "ALPHA"}}, ""); err != nil {
		t.Fatal(err)
	}
	_, err := PatchFile(path, []PatchChange{{Old: "alpha", New: "gamma"}}, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	if msg := err.Error(); !strings.Contains(msg, "patched 1 time(s) earlier in this session") {
		t.Fatalf("earlier write not reported: %s", msg)
	}
}

// With no verdict and no session history there is still one useful sentence.
func TestPatchFile_UnexplainedMissSaysReRead(t *testing.T) {
	path := writeTemp(t, "plain.txt", "alpha\nbeta\ngamma\n")
	_, err := PatchFile(path, []PatchChange{{Old: "delta", New: "x"}}, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	if msg := err.Error(); !strings.Contains(msg, "re-read the file before patching") {
		t.Fatalf("no re-read hint: %s", msg)
	}
}

// ...but a call that already got a precise repair is not padded with it.
func TestPatchFile_PreciseVerdictNotPaddedWithReRead(t *testing.T) {
	path := writeTemp(t, "ws.txt", "func main() {\n\tprintln(\"hi\")\n}\n")
	_, err := PatchFile(path, []PatchChange{{Old: "func main() {\n    println(\"hi\")\n}", New: "x"}}, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "whitespace") {
		t.Fatalf("expected a whitespace verdict: %s", msg)
	}
	if strings.Contains(msg, "re-read the file before patching") {
		t.Fatalf("generic hint added on top of a precise verdict: %s", msg)
	}
}

// Regression: the patch success path is untouched — no diagnostics, no warning,
// identical result. The median `old` is 6.8% of the file, so a "your old is
// large" hint on success would only discourage correct calls.
func TestPatchFile_SuccessPathUnchanged(t *testing.T) {
	path := writeTemp(t, "ok.txt", "alpha\nbeta\ngamma\n")
	res, err := PatchFile(path, []PatchChange{
		{Old: "alpha", New: "ALPHA"},
		{Old: "gamma", New: "GAMMA"},
	}, "")
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if res.Replacements != 2 || !res.Changed {
		t.Fatalf("got %+v", res)
	}
	if got, _ := os.ReadFile(path); string(got) != "ALPHA\nbeta\nGAMMA\n" {
		t.Fatalf("content = %q", got)
	}
}

// The whole `old` present but in a file bigger than diagMaxContent: diagnostics
// bow out rather than allocating copies of a huge file.
func TestPatchFailureHint_SkipsHugeFiles(t *testing.T) {
	content := strings.Repeat("a", diagMaxContent+1)
	if h := patchFailureHint("", content, []PatchChange{{Old: "not-here-at-all"}}, 0, 1, 0); h != "" {
		t.Fatalf("diagnostics ran on an oversized file: %q", h)
	}
}

// Case 7 — cost. Diagnostics run only on the failure path, but a failed patch
// against a large file must not stall the loop.
func benchContent100KB() string {
	var sb strings.Builder
	for sb.Len() < 100*1024 {
		fmt.Fprintf(&sb, "  const line%06d = someValue * %d;\n", sb.Len(), sb.Len()%97)
	}
	return sb.String()
}

func BenchmarkPatchFailureHint_100KB(b *testing.B) {
	content := benchContent100KB()
	// `old` is a 2.5 KB block from the middle whose tail diverges: the most
	// expensive shape (prefix search runs to full depth).
	old := content[40000:42500] + "TAIL-THAT-DOES-NOT-EXIST"
	changes := []PatchChange{{Old: "a"}, {Old: "b"}, {Old: "c"}, {Old: old}}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if h := patchFailureHint("", content, changes, 3, 1, 0); h == "" {
			b.Fatal("empty hint")
		}
	}
}

// The wrong-file shape, worst case for the new branches: change 0, so the
// whole-call scan runs, and every change misses so it runs to the end.
func BenchmarkPatchFailureHint_WrongFile_100KB(b *testing.B) {
	content := benchContent100KB()
	changes := []PatchChange{
		{Old: htmlBlock("cart", 2500)},
		{Old: htmlBlock("summary", 2500)},
		{Old: htmlBlock("footer", 500)},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if h := patchFailureHint("", content, changes, 0, 1, 0); h == "" {
			b.Fatal("empty hint")
		}
	}
}

// The bare failure (no diagnostics) for comparison: this is what the model used
// to get for the same call.
func BenchmarkPatchMatchOnly_100KB(b *testing.B) {
	content := benchContent100KB()
	old := content[40000:42500] + "TAIL-THAT-DOES-NOT-EXIST"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = strings.Count(content, old)
	}
}
