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

	"supercli/internal/tools/fileops"
)

func TestReadLinesOversizedRangeReturnsEvidence(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "limits.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("value\n", 24)+"RetryLimit=6842\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tool := NewReadLines(root).Spec()
	for _, end := range []int{521, 10000, math.MaxInt} {
		args, _ := json.Marshal(map[string]any{"file": "limits.txt", "from": 21, "to": end})
		result, err := tool.Fn(context.Background(), args)
		if err != nil || result.Err != nil || !strings.Contains(result.Text, "RetryLimit=6842") || !strings.Contains(result.Text, "[end of file at line 25]") {
			t.Fatalf("end=%d: %+v %v", end, result, err)
		}
		if strings.Contains(result.Text, "not read") {
			t.Fatalf("false omission at EOF: %s", result.Text)
		}
	}
	// The strict library contract remains available to non-tool callers.
	if _, err := fileops.ReadLines(path, 21, 521); err == nil {
		t.Fatal("library range cap changed")
	}
}

func TestReadLinesOversizedRangeReportsUnreadTail(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for n := 1; n <= 600; n++ {
		fmt.Fprintf(&b, "unique_line_%d\n", n)
	}
	if err := os.WriteFile(filepath.Join(root, "limits.txt"), []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	tool := NewReadLines(root).Spec()
	for _, end := range []int{521, 600, 10000} {
		args, _ := json.Marshal(map[string]any{"file": "limits.txt", "from": 21, "to": end})
		result, err := tool.Fn(context.Background(), args)
		marker := fmt.Sprintf("[range capped at 500 lines; requested lines 521-%d not read]", end)
		if err != nil || result.Err != nil || !strings.Contains(result.Text, marker) || strings.Contains(result.Text, "end of file") {
			t.Fatalf("%+v %v", result, err)
		}
		if strings.Count(result.Text, " | ") != 500 || !strings.Contains(result.Text, " 520 | unique_line_520\n") || strings.Contains(result.Text, "unique_line_521") {
			t.Fatal("incorrect capped range")
		}
		remaining, _ := json.Marshal(map[string]any{"file": "limits.txt", "from": 521, "to": 521})
		next, nextErr := tool.Fn(context.Background(), remaining)
		if nextErr != nil || next.Err != nil || !strings.Contains(next.Text, "unique_line_521") {
			t.Fatalf("tail unavailable: %+v %v", next, nextErr)
		}
	}
	normal, _ := json.Marshal(map[string]any{"file": "limits.txt", "from": 21, "to": 520})
	got, err := tool.Fn(context.Background(), normal)
	lines, eof, wantErr := fileops.ReadLinesBoundedWithEOF(context.Background(), filepath.Join(root, "limits.txt"), 21, 520, maxReadLineKeep)
	if err != nil || got.Err != nil || wantErr != nil || got.Text != renderLinesWithEOF(lines, eof) {
		t.Fatalf("valid range changed: %+v %v %v", got, err, wantErr)
	}
}

func TestReadLinesRangeCapPreservesErrorsAndOutputOmissions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "long.txt"), []byte(strings.Repeat(strings.Repeat("x", 1900)+"\n", 600)), 0600); err != nil {
		t.Fatal(err)
	}
	tool := NewReadLines(root).Spec()
	for _, r := range [][2]int{{5, 4}, {1, 0}, {1000, 1500}, {math.MaxInt - 600, math.MaxInt}} {
		args, _ := json.Marshal(map[string]any{"file": "long.txt", "from": r[0], "to": r[1]})
		got, err := tool.Fn(context.Background(), args)
		if err != nil || got.Err == nil {
			t.Fatalf("bad request succeeded: %v %+v %v", r, got, err)
		}
	}
	args := json.RawMessage("{\"file\":\"long.txt\",\"from\":1,\"to\":501}")
	got, err := tool.Fn(context.Background(), args)
	if err != nil || got.Err != nil || !strings.Contains(got.Text, "more line(s) not shown") || !strings.Contains(got.Text, "requested lines 501-501 not read") {
		t.Fatalf("lost omission: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err = tool.Fn(ctx, args)
	if err != nil || got.Err == nil {
		t.Fatalf("cancellation lost: %+v %v", got, err)
	}
}
