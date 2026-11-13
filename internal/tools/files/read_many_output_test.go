package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/tools/core"
	"supercli/internal/tools/fileops"
)

// writeModules creates count Go files of about size bytes each, every one
// with a function whose body sits in the middle of the file.
func writeModules(t *testing.T, dir string, count, funcs int) []string {
	t.Helper()
	var names []string
	for i := 0; i < count; i++ {
		var b strings.Builder
		fmt.Fprintf(&b, "package mod%02d\n\n", i)
		for f := 0; f < funcs; f++ {
			fmt.Fprintf(&b, "// F%d_%d documents a rule.\nfunc F%d_%d(x int) int {\n\tif x > %d {\n\t\treturn x * %d // BODY_%02d_%d\n\t}\n\treturn x\n}\n\n", i, f, i, f, f, f+2, i, f)
		}
		name := fmt.Sprintf("mod%02d.go", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

var readManySummaryRE = regexp.MustCompile(`\[read_many: (\d+) ok, (\d+) failed\]$`)

// The S1 regression: twelve mid-sized files must never come back with the
// middle of each one cut out. Every returned file is whole; the rest are
// named with a follow-up call that reads exactly them.
func TestReadManyReturnsWholeFilesAndNamesTheRest(t *testing.T) {
	dir := t.TempDir()
	names := writeModules(t, dir, 12, 12) // ~2.6 KB each, ~31 KB total
	args, _ := json.Marshal(map[string]any{"reads": strings.Join(names, " | ")})
	for _, window := range []int{0, 16384, 32768, 200000} {
		ctx := core.WithModelWindow(context.Background(), window)
		result, err := NewReadMany(dir).execute(ctx, args)
		if err != nil || result.Err != nil {
			t.Fatalf("window=%d: %v %v", window, err, result.Err)
		}
		budget := core.ReadBatchBudget(ctx)
		if len(result.Text) > budget+256 {
			t.Fatalf("window=%d: %d bytes over budget %d", window, len(result.Text), budget)
		}
		if strings.Contains(result.Text, "omitted") || result.ModelPreview != "" || result.RetainedText != "" {
			t.Fatalf("window=%d: middle trimming came back", window)
		}
		m := readManySummaryRE.FindStringSubmatch(result.Text)
		if m == nil {
			t.Fatalf("window=%d: summary is not the last line:\n%s", window, result.Text)
		}
		returned := 0
		var missing []string
		for i, name := range names {
			if strings.Contains(result.Text, "] "+name+":") {
				returned++
				for f := 0; f < 12; f++ {
					if !strings.Contains(result.Text, fmt.Sprintf("BODY_%02d_%d", i, f)) {
						t.Fatalf("window=%d: %s returned without function %d", window, name, f)
					}
				}
				continue
			}
			missing = append(missing, name+":1-300")
		}
		if fmt.Sprint(returned) != m[1] || returned == 0 {
			t.Fatalf("window=%d: returned=%d summary=%s", window, returned, m[0])
		}
		if len(missing) == 0 {
			if strings.Contains(result.Text, "incomplete") {
				t.Fatalf("window=%d: complete batch flagged incomplete", window)
			}
			continue
		}
		next := fmt.Sprintf("read_many reads=%q", strings.Join(missing, " | "))
		if !strings.Contains(result.Text, next) || !strings.Contains(result.Text, fmt.Sprintf("[incomplete: %d range(s) not returned", len(missing))) {
			t.Fatalf("window=%d: continuation missing %s:\n%s", window, next, result.Text[len(result.Text)-600:])
		}
		// The model-facing view is the same text: nothing is compacted again.
		visible := core.NewOutputStore().ModelContentContext(ctx, "read_many", result)
		if visible != result.Text {
			t.Fatalf("window=%d: output store re-trimmed a budgeted batch", window)
		}
	}
	// A large window returns a moderate batch whole in one call.
	small := t.TempDir()
	names = writeModules(t, small, 12, 10)
	args, _ = json.Marshal(map[string]any{"reads": strings.Join(names, " | ")})
	ctx := core.WithModelWindow(context.Background(), 200000)
	result, _ := NewReadMany(small).execute(ctx, args)
	if !strings.HasSuffix(result.Text, "[read_many: 12 ok, 0 failed]") {
		t.Fatalf("large window did not return all files: %s", result.Text[len(result.Text)-300:])
	}
}

// One file larger than the budget is cut at a declaration boundary, never in
// a function body, and the remaining lines are named.
func TestReadManySingleOversizedFileCutsAtDeclaration(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("package big\n\n")
	for f := 0; f < 40; f++ {
		fmt.Fprintf(&b, "// F%d documents %s.\nfunc F%d(x int) int {\n\tif x > %d {\n\t\treturn x // %s\n\t}\n\treturn x\n}\n\n", f, strings.Repeat("a rule ", 8), f, f, strings.Repeat("body ", 8))
	}
	if err := os.WriteFile(filepath.Join(dir, "mod00.go"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := core.WithModelWindow(context.Background(), 8192)
	result, err := NewReadMany(dir).execute(ctx, []byte(`{"reads":"mod00.go"}`))
	if err != nil || result.Err != nil {
		t.Fatal(err, result.Err)
	}
	m := regexp.MustCompile(`== \[1\] mod00\.go:1-(\d+) ==`).FindStringSubmatch(result.Text)
	cont := regexp.MustCompile(`\[lines (\d+)-300 not returned`).FindStringSubmatch(result.Text)
	if m == nil || cont == nil {
		t.Fatalf("missing partial header or note:\n%s", result.Text)
	}
	var last, next int
	fmt.Sscan(m[1], &last)
	fmt.Sscan(cont[1], &next)
	if next != last+1 || !strings.Contains(result.Text, fmt.Sprintf(`read_many reads="mod00.go:%d-300"`, next)) {
		t.Fatalf("continuation does not resume at %d:\n%s", last+1, result.Text)
	}
	lines := strings.Split(result.Text, "\n")
	var lastRow string
	for _, line := range lines {
		if strings.Contains(line, " | ") {
			lastRow = line
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(lastRow), "|") && !strings.HasSuffix(lastRow, "| }") {
		t.Fatalf("cut inside a declaration, last row %q", lastRow)
	}
	if !strings.HasSuffix(result.Text, "[read_many: 1 ok, 0 failed]") {
		t.Fatal("partial file is still a returned section")
	}
}

func TestReadManyOverCapIsContinuedNotRejected(t *testing.T) {
	dir := t.TempDir()
	var names []string
	for i := 0; i < 16; i++ {
		name := fmt.Sprintf("f%02d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("v\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	args, _ := json.Marshal(map[string]any{"reads": strings.Join(names, " | ")})
	result, err := NewReadMany(dir).execute(context.Background(), args)
	if err != nil || result.Err != nil {
		t.Fatal(err, result.Err)
	}
	if !strings.HasSuffix(result.Text, "[read_many: 12 ok, 0 failed]") ||
		!strings.Contains(result.Text, `read_many reads="f12.txt:1-300 | f13.txt:1-300 | f14.txt:1-300 | f15.txt:1-300"`) {
		t.Fatalf("over-cap ranges were not continued:\n%s", result.Text)
	}
	// Globs past the cap are continued the same way.
	result, _ = NewReadMany(dir).execute(context.Background(), []byte(`{"reads":"*.txt:1-1"}`))
	if result.Err != nil || !strings.Contains(result.Text, "4 range(s) not returned") || !strings.Contains(result.Text, "f15.txt:1-1") {
		t.Fatalf("glob overflow: %+v", result)
	}
}

func TestReadManySmallResultStaysInlineWithEOF(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\r\ntwo\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"reads": "a.txt:1-2"})
	result, err := NewReadMany(dir).execute(context.Background(), args)
	want := "== [1] a.txt:1-2 ==\n   1 | one\n   2 | two\n[end of file at line 2]\n[read_many: 1 ok, 0 failed]"
	if err != nil || result.Err != nil || result.Text != want || result.RetainedText != "" || result.ModelPreview != "" {
		t.Fatalf("changed small result: %+v %v", result, err)
	}
	if got := core.NewOutputStore().ModelContent("read_many", result); got != want {
		t.Fatal(got)
	}
}

func TestReadManyErrorsAlwaysReportedAndBounded(t *testing.T) {
	outcomes := make([]readManyOutcome, 3)
	big := make([]fileops.LineRange, 300)
	for i := range big {
		big[i] = fileops.LineRange{Number: i + 1, Content: strings.Repeat("zażółć", 10)}
	}
	outcomes[0] = readManyOutcome{request: readManyRequest{File: "big.txt", From: 1, To: 300}, lines: big}
	outcomes[1] = readManyOutcome{request: readManyRequest{File: "big2.txt", From: 1, To: 300}, lines: big}
	outcomes[2] = readManyOutcome{request: readManyRequest{File: strings.Repeat("目录", 160) + "/missing.go", From: 1, To: 10}, err: errors.New("not_found " + strings.Repeat("x", 3000))}
	result := renderReadMany(outcomes, nil, 12*1024)
	if result.Err != nil || !utf8.ValidString(result.Text) {
		t.Fatal("partial success became an error or invalid text")
	}
	if !strings.Contains(result.Text, "error: not_found") || !strings.HasSuffix(result.Text, "[read_many: 1 ok, 1 failed]") {
		t.Fatalf("lost failure or summary:\n%s", result.Text[len(result.Text)-400:])
	}
	if len(result.Text) > 12*1024+2048 {
		t.Fatalf("unbounded: %d", len(result.Text))
	}
}

func BenchmarkReadManyRendering(b *testing.B) {
	lines := make([]fileops.LineRange, 100)
	for i := range lines {
		lines[i] = fileops.LineRange{Number: i + 1, Content: strings.Repeat("z", 40)}
	}
	out := make([]readManyOutcome, 12)
	for i := range out {
		out[i] = readManyOutcome{request: readManyRequest{File: fmt.Sprintf("file%d.txt", i), From: 1, To: 100}, lines: lines}
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = renderReadMany(out, nil, 32*1024)
	}
}
