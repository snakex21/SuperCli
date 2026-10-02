package search

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/tools/core"
)

func groupedSearchFixture(t testing.TB) (string, string) {
	t.Helper()
	root := t.TempDir()
	var body strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&body, "BudgetLimit_%02d = %d // zażółć\n", i, 7000+i)
	}
	name := strings.Repeat("project/", 5) + "limits/long_configuration_name.go"
	writeSearchFixture(t, root, name, body.String())
	return root, name
}

func TestGroupedSearchPreviewKeepsEveryRowWithoutStorage(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	root, name := groupedSearchFixture(t)
	tool := NewSearchCode(root)
	for _, max := range []int{15, 21} {
		args, _ := json.Marshal(map[string]any{"query": "BudgetLimit", "max": max, "context": 0})
		result, err := tool.run(context.Background(), args)
		if err != nil || result.Err != nil || result.ModelText == "" {
			t.Fatalf("error=%v/%v preview=%q", err, result.Err, result.ModelText)
		}
		count := min(max, 20)
		original := result.Text
		store := core.NewOutputStore()
		view := store.ModelContent("search_code", result)
		if len(view) >= len(original) || len(result.ModelText) > core.ModelOutputPreviewBytes || !utf8.ValidString(view) {
			t.Fatalf("preview=%d model=%d original=%d", len(result.ModelText), len(view), len(original))
		}
		if !strings.Contains(view, "numbers are source line numbers") ||
			strings.Count(result.ModelText, name) != 1 {
			t.Fatalf("missing file reference: %s", view)
		}
		last := -1
		for i := 1; i <= count; i++ {
			content := fmt.Sprintf("BudgetLimit_%02d = %d // zażółć", i, 7000+i)
			full := fmt.Sprintf("%s:%d:%s", name, i, content)
			row := fmt.Sprintf("%d | %s", i, content)
			pos := strings.Index(result.ModelText, row)
			if !strings.Contains(original, full) || pos <= last {
				t.Fatalf("row %d changed or reordered: %s", i, view)
			}
			last = pos
		}
		if (max <= 20) != strings.Contains(view, searchLimitNotice(max)) {
			t.Fatal("match cap notice changed")
		}
		if core.StoredOutputHandle(view) != "" || view != result.ModelText {
			t.Fatal("complete small representation forced output storage")
		}
		if result.Text != original || result.RetainedText != "" {
			t.Fatal("UI/location output changed")
		}
		t.Logf("matches=%d original=%d model=%d saved=%d", count, len(original), len(view), len(original)-len(view))
	}
}

func TestGroupedSearchPreviewRealRGParity(t *testing.T) {
	rg := os.Getenv("SUPERCLI_TEST_RG")
	if rg == "" {
		t.Skip("set SUPERCLI_TEST_RG")
	}
	root, _ := groupedSearchFixture(t)
	args := json.RawMessage(`{"query":"BudgetLimit","context":0,"max":15}`)
	t.Setenv("PATH", filepath.Dir(rg))
	native, err := NewSearchCode(root).run(context.Background(), args)
	if err != nil || native.Err != nil {
		t.Fatalf("%+v %v", native, err)
	}
	t.Setenv("PATH", t.TempDir())
	fallback, err := NewSearchCode(root).run(context.Background(), args)
	if err != nil || fallback.Err != nil || native.ModelText == "" ||
		native.ModelText != fallback.ModelText || native.Text != fallback.Text {
		t.Fatalf("backends diverged: native=%+v fallback=%+v err=%v", native, fallback, err)
	}
}

func TestGroupedSearchPreviewKeepsOrdinaryAndOversizedResults(t *testing.T) {
	tool := NewSearchCode(".")
	for _, records := range [][]searchRecord{
		{{path: "a.go", line: 1, text: "needle"}, {path: "a.go", line: 2, text: "needle"}, {path: "a.go", line: 3, text: "needle"}},
		{{path: "a.go", line: 1, text: "needle"}, {path: "a.go", line: 2, text: "needle"}, {path: "a.go", line: 3, text: "needle"}, {path: "a.go", line: 4, text: "needle"}},
		{{path: "a.go", line: 1, text: "needle"}, {path: "b.go", line: 2, text: "needle"}, {path: "c.go", line: 3, text: "needle"}, {path: "d.go", line: 4, text: "needle"}},
		{{path: "long/path.go", line: 1, text: strings.Repeat("x", 4500)}, {path: "long/path.go", line: 2, text: "needle"}, {path: "long/path.go", line: 3, text: "needle"}, {path: "long/path.go", line: 4, text: "needle"}},
	} {
		var lines []string
		for _, hit := range records {
			lines = append(lines, fmt.Sprintf("%s:%d:%s", tool.displayPath(hit.path), hit.line, hit.text))
		}
		text := strings.Join(lines, "\n")
		got := tool.previewSearchHits(Result{Text: text}, &searchContext{records: records}, "needle")
		if got.ModelPreview != "" || got.ModelText != "" || got.Text != text {
			t.Fatalf("ordinary output changed: %+v", got)
		}
	}
	records := make([]searchRecord, 10)
	var lines []string
	for i := range records {
		records[i] = searchRecord{path: strings.Repeat("deep/", 20) + "needle.go", line: i + 1, text: "needle"}
		lines = append(lines, fmt.Sprintf("%s:%d:needle", records[i].path, i+1))
	}
	fail := Result{Text: strings.Join(lines, "\n"), Err: fmt.Errorf("search incomplete")}
	got := tool.previewSearchHits(fail, &searchContext{records: records}, "needle")
	if got.ModelPreview != "" || got.ModelText != "" || got.Text != fail.Text || got.Err != fail.Err {
		t.Fatal("error output was grouped")
	}
}

func TestGroupedSearchPreviewInterleavedFilesAndHeaderEscaping(t *testing.T) {
	tool := NewSearchCode(".")
	paths := []string{strings.Repeat("deep/", 20) + "one:quoted\".go", strings.Repeat("deep/", 20) + "two.go"}
	var records []searchRecord
	var lines []string
	for _, path := range []string{paths[0], paths[1], paths[0]} {
		for i := 1; i <= 4; i++ {
			records = append(records, searchRecord{path: path, line: i, text: fmt.Sprintf("needle=%d", len(records))})
			lines = append(lines, fmt.Sprintf("%s:%d:%s", path, i, records[len(records)-1].text))
		}
	}
	got := tool.previewSearchHits(Result{Text: strings.Join(lines, "\n")}, &searchContext{records: records}, "needle")
	if got.ModelText == "" || strings.Count(got.ModelText, "== ") != 3 ||
		!strings.Contains(got.ModelText, `one:quoted\".go" ==`) {
		t.Fatalf("header/path lost: %s", got.ModelText)
	}
	last := -1
	for i := range records {
		pos := strings.Index(got.ModelText, fmt.Sprintf("needle=%d\n", i))
		if pos < 0 && i == len(records)-1 {
			pos = strings.Index(got.ModelText, fmt.Sprintf("needle=%d", i))
		}
		if pos <= last {
			t.Fatalf("row %d reordered", i)
		}
		last = pos
	}
}

var groupedPreviewBenchResult Result

func BenchmarkGroupedSearchPreview(b *testing.B) {
	root, _ := groupedSearchFixture(b)
	tool := NewSearchCode(root)
	preview := &searchContext{}
	result, err := tool.fallback(context.Background(), root, "BudgetLimit", 50, preview)
	if err != nil || result.Err != nil {
		b.Fatalf("%+v %v", result, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		groupedPreviewBenchResult = tool.previewSearchHits(result, preview, "BudgetLimit")
	}
}

func TestGroupedLargeSearchRetainsOriginalLocations(t *testing.T) {
	tool := NewSearchCode(".")
	path := strings.Repeat("deep/", 36) + "limits.go"
	var records []searchRecord
	var b strings.Builder
	for i := 1; i <= 50; i++ {
		content := fmt.Sprintf("BudgetLimit_%02d = %d", i, 7000+i)
		records = append(records, searchRecord{path: path, line: i, text: content})
		fmt.Fprintf(&b, "%s:%d:%s\n", path, i, content)
	}
	original := strings.TrimSuffix(b.String(), "\n") + "\n" + searchLimitNotice(50)
	result := tool.previewSearchHits(Result{Text: original}, &searchContext{records: records, limit: 50}, "BudgetLimit")
	if result.ModelPreview == "" || result.ModelText != "" || result.Text != original {
		t.Fatalf("large result contract changed: text=%d preview=%d", len(result.Text), len(result.ModelPreview))
	}
	store := core.NewOutputStore()
	baseline := store.ModelContent("search_code", Result{Text: original})
	view := store.ModelContent("search_code", result)
	for i := 1; i <= 50; i++ {
		if !strings.Contains(view, fmt.Sprintf("%d | BudgetLimit_%02d = %d", i, i, 7000+i)) {
			t.Fatalf("lost matching row %d", i)
		}
	}
	handle := core.StoredOutputHandle(view)
	raw, _ := json.Marshal(map[string]any{"handle": handle, "query": "BudgetLimit_50"})
	saved, err := store.ReadOutputTool().Fn(context.Background(), raw)
	if err != nil || saved.Err != nil || !strings.Contains(saved.Text, path+":50:BudgetLimit_50 = 7050") ||
		!strings.Contains(view, searchLimitNotice(50)) {
		t.Fatalf("saved location unavailable: %+v %v", saved, err)
	}
	t.Logf("large original=%d old-model=%d grouped-model=%d saved-model=%d",
		len(original), len(baseline), len(view), len(baseline)-len(view))
}
