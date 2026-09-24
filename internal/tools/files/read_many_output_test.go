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
)

func TestReadManyModelPreviewIncludesEveryFileAndError(t *testing.T) {
	dir := t.TempDir()
	var requests []readManyRequest
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("file%d.txt", i)
		requests = append(requests, readManyRequest{File: name, From: 1, To: 100})
		if i == 5 {
			continue
		}
		var body strings.Builder
		for n := 1; n <= 100; n++ {
			fmt.Fprintf(&body, "file%d-line%d %s\n", i, n, strings.Repeat("ż", 32))
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body.String()), 0600); err != nil {
			t.Fatal(err)
		}
	}
	args, _ := json.Marshal(map[string]any{"reads": requests})
	result, err := NewReadMany(dir).execute(context.Background(), args)
	if err != nil || result.Err != nil {
		t.Fatalf("%v %v", err, result.Err)
	}
	store := core.NewOutputStore()
	preview := store.ModelContent("read_many", result)
	legacyPreview := core.NewOutputStore().Compact("read_many", result.Text)
	legacySections := 0
	for i := 0; i < 12; i++ {
		if strings.Contains(legacyPreview, fmt.Sprintf("== [%d] file%d.txt:1-100 ==", i+1, i)) {
			legacySections++
		}
	}
	t.Logf("batch preview: old=%d bytes/%d headers; new=%d bytes/12 headers; retained=%d bytes", len(legacyPreview), legacySections, len(preview), len(result.RetainedText))
	if len(result.ModelPreview) > core.ModelOutputPreviewBytes || len(preview) > core.ModelOutputPreviewBytes+200 || !utf8.ValidString(preview) {
		t.Fatalf("preview too large or invalid: %d bytes", len(preview))
	}
	previous := -1
	for i := 0; i < 12; i++ {
		header := fmt.Sprintf("== [%d] file%d.txt:1-100 ==", i+1, i)
		pos := strings.Index(preview, header)
		if pos <= previous {
			t.Fatalf("missing/out of order %s:\n%s", header, preview)
		}
		previous = pos
		if i != 5 && !strings.Contains(preview, fmt.Sprintf("file%d-line1 ", i)) {
			t.Fatalf("missing file %d excerpt", i)
		}
	}
	if !strings.Contains(preview, "error: not_found") || !strings.Contains(preview, "11 ok, 1 failed") {
		t.Fatal(preview)
	}
	// The omitted middle survives independently of the source file.
	if err := os.Remove(filepath.Join(dir, "file6.txt")); err != nil {
		t.Fatal(err)
	}
	handle := regexp.MustCompile(`handle=(out_[a-f0-9]+)`).FindStringSubmatch(preview)
	if len(handle) != 2 {
		t.Fatal("missing output handle")
	}
	query, _ := json.Marshal(map[string]any{"handle": handle[1], "query": "file6-line50 "})
	read, err := store.ReadOutputTool().Fn(context.Background(), query)
	if err != nil || read.Err != nil || !strings.Contains(read.Text, "file6-line50 ") {
		t.Fatalf("lost retained range: %v %v %s", err, read.Err, read.Text)
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

func TestReadManyPreviewBoundsLongUnicodeNamesAndUnevenSections(t *testing.T) {
	for _, count := range []int{1, 3, 12} {
		outcomes := make([]readManyOutcome, count)
		for i := range outcomes {
			outcomes[i] = readManyOutcome{request: readManyRequest{File: strings.Repeat("目录", 160) + fmt.Sprintf("/last%d.go", i), From: 100, To: 399}, text: strings.Repeat(strings.Repeat("zażółć", 16)+"\n", 300)}
			if i == count/2 {
				outcomes[i].err = errors.New("permission " + strings.Repeat("目录", 160))
			}
		}
		result := renderReadMany(outcomes)
		preview := readManyPreview(outcomes, "[summary]")
		if len(preview) > core.ModelOutputPreviewBytes || !utf8.ValidString(preview) {
			t.Fatalf("invalid preview: %d", len(preview))
		}
		for i := range outcomes {
			if !strings.Contains(preview, fmt.Sprintf("/last%d.go:100-399 ==", i)) {
				t.Fatalf("lost path %d", i)
			}
		}
		if !strings.Contains(preview, "error: permission") || (count > 1 && result.RetainedText == "") {
			t.Fatal("lost error or retained output")
		}
	}
}

func TestReadManySinglePartialAndFailedBatchesKeepCompactPreview(t *testing.T) {
	cases := map[string][]readManyOutcome{
		"single":  {{request: readManyRequest{File: "single.txt", From: 1, To: 300}, text: strings.Repeat("s", 9000)}},
		"partial": {{request: readManyRequest{File: "large.txt", From: 1, To: 300}, text: strings.Repeat("a", 9000)}, {request: readManyRequest{File: "small.txt", From: 1, To: 10}, text: strings.Repeat("b", 1000)}},
		"failed":  {{request: readManyRequest{File: "large.txt", From: 1, To: 300}, text: strings.Repeat("a", 8180)}, {request: readManyRequest{File: "missing.txt", From: 1, To: 10}, err: os.ErrNotExist}},
	}
	for name, outcomes := range cases {
		t.Run(name, func(t *testing.T) {
			result := renderReadMany(outcomes)
			if len(result.Text) <= core.ModelOutputInlineBytes {
				t.Fatalf("fixture too small: %d", len(result.Text))
			}
			if result.ModelPreview == "" {
				t.Fatal("non-complete batch lost its compact preview")
			}
			visible := core.NewOutputStore().ModelContent("read_many", result)
			if len(visible) > core.ModelOutputPreviewBytes+200 || !strings.Contains(visible, "handle=") {
				t.Fatalf("unbounded or unretained preview: %d bytes", len(visible))
			}
		})
	}
}
