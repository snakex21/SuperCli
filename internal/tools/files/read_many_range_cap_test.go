package files

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadManyOversizedRangesReturnBoundedEvidence(t *testing.T) {
	root := t.TempDir()
	var body strings.Builder
	for n := 1; n <= 650; n++ {
		fmt.Fprintf(&body, "value_%d\n", n)
	}
	if err := os.WriteFile(filepath.Join(root, "netplay.go"), []byte(body.String()), 0600); err != nil {
		t.Fatal(err)
	}
	tool := NewReadMany(root)
	for _, reads := range []any{
		"netplay.go:300-600",
		[]map[string]any{{"file": "netplay.go", "from": 300, "to": 600}},
		"*.go:300-600",
	} {
		raw, _ := json.Marshal(map[string]any{"reads": reads})
		result, err := tool.execute(context.Background(), raw)
		for _, want := range []string{
			"netplay.go:300-599", " 300 | value_300\n", " 599 | value_599\n",
			"[range capped at 300 lines; requested lines 600-600 not read]",
			"[read_many: 1 ok, 0 failed]",
		} {
			if err != nil || result.Err != nil || !strings.Contains(result.Text, want) {
				t.Fatalf("reads=%v missing %q: %+v %v", reads, want, result, err)
			}
		}
		if strings.Count(result.Text, " | ") != 300 || strings.Contains(result.Text, "value_600") || strings.Contains(result.Text, "end of file") {
			t.Fatal("range cap was expanded or tail was claimed complete")
		}
	}
	next, err := tool.execute(context.Background(), []byte(`{"reads":"netplay.go:600-600"}`))
	if err != nil || next.Err != nil || !strings.Contains(next.Text, " 600 | value_600\n") {
		t.Fatalf("unread tail inaccessible: %+v %v", next, err)
	}
}

func TestReadManyOversizedRangeAtEOFAndPartialFailures(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "short.go"), []byte("boundary=100\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, end := range []int{301, 360, math.MaxInt} {
		reads := fmt.Sprintf("short.go:1-%d | missing.go:1-%d | short.go:5-4", end, end)
		raw, _ := json.Marshal(map[string]string{"reads": reads})
		result, err := NewReadMany(root).execute(context.Background(), raw)
		for _, want := range []string{"boundary=100", "[end of file at line 1]", "error: not_found", "error: invalid range 5-4", "[read_many: 1 ok, 2 failed]"} {
			if err != nil || result.Err != nil || !strings.Contains(result.Text, want) {
				t.Fatalf("end=%d missing %q: %+v %v", end, want, result, err)
			}
		}
		if strings.Contains(result.Text, "not read") || strings.Contains(result.Text, "exceeds cap") {
			t.Fatalf("false omission/error at EOF: %s", result.Text)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := NewReadMany(root).execute(ctx, []byte(`{"reads":"short.go:1-360"}`))
	if err != nil || !strings.Contains(result.Text, "context canceled") || !strings.Contains(result.Text, "0 ok, 1 failed") {
		t.Fatalf("lost cancellation: %+v %v", result, err)
	}
}

func TestReadManyRangeClampPreservesOutputBounds(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(strings.Repeat(strings.Repeat("x", 1900)+"\n", 600)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := NewReadMany(root).execute(context.Background(), []byte(`{"reads":"a.txt:1-600 | b.txt:1-600"}`))
	if err != nil || result.Err != nil || !strings.Contains(result.Text, "2 ok, 0 failed") {
		t.Fatalf("%+v %v", result, err)
	}
	if len(result.Text) > maxReadManyBytes+256 || len(result.ModelPreview) > 4096 || !strings.Contains(result.Text, "omitted_bytes") {
		t.Fatal("existing byte bounds were lost")
	}
	for _, text := range []string{result.Text, result.RetainedText, result.ModelPreview} {
		if strings.Count(text, "[range capped at 300 lines; requested lines 301-600 not read]") != 2 {
			t.Fatal("preview or retained result lost capped tails")
		}
	}
}

func TestReadManyDefaultRangeSaturatesAtMaxInt(t *testing.T) {
	raw, _ := json.Marshal([]map[string]any{{"file": "tail.go", "from": math.MaxInt - 10}})
	got, err := decodeReadManyRequests(raw)
	if err != nil || len(got) != 1 || got[0].To != math.MaxInt {
		t.Fatalf("default range overflow: %+v %v", got, err)
	}
}
