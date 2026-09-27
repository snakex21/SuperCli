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

func TestReadLinesMissingEndUsesBoundedDefault(t *testing.T) {
	root := t.TempDir()
	var content strings.Builder
	for i := 1; i <= 700; i++ {
		fmt.Fprintf(&content, "line_%d\n", i)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte(content.String()), 0600); err != nil {
		t.Fatal(err)
	}
	tool := NewReadLines(root).Spec()
	for _, tc := range []struct {
		raw  string
		from int
	}{
		{`{"file":"notes.txt"}`, 1},
		{`{"file":"notes.txt","from":21}`, 21},
		{`{"file":"notes.txt","from":0}`, 1},
	} {
		result, err := tool.Fn(context.Background(), json.RawMessage(tc.raw))
		if err != nil || result.Err != nil {
			t.Fatalf("%s: %v %v", tc.raw, err, result.Err)
		}
		if strings.Count(result.Text, " | ") != 300 || !strings.Contains(result.Text, fmt.Sprintf(" | line_%d\n", tc.from)) || !strings.Contains(result.Text, fmt.Sprintf(" | line_%d\n", tc.from+299)) {
			t.Fatalf("default range wrong: %s", result.Text)
		}
		if strings.Contains(result.Text, fmt.Sprintf(" | line_%d\n", tc.from+300)) || !strings.Contains(result.Text, fmt.Sprintf("continue from line %d", tc.from+300)) {
			t.Fatalf("unread tail missing or unbounded: %s", result.Text)
		}
	}
	end, err := tool.Fn(context.Background(), json.RawMessage(`{"file":"notes.txt","from":695}`))
	if err != nil || end.Err != nil || !strings.Contains(end.Text, "end of file at line 700") || strings.Contains(end.Text, "continue from") {
		t.Fatalf("EOF: %+v %v", end, err)
	}
}

func TestReadLinesMissingEndPreservesInvalidRangesAndReadErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "short.txt"), []byte("small file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tool := NewReadLines(root).Spec()
	for _, raw := range []string{
		`{"file":"short.txt","to":0}`,
		`{"file":"short.txt","from":5,"to":4}`,
		fmt.Sprintf(`{"file":"short.txt","from":%d}`, math.MaxInt),
		`{"file":"missing.txt"}`,
	} {
		result, err := tool.Fn(context.Background(), json.RawMessage(raw))
		if err != nil || result.Err == nil {
			t.Fatalf("%s: %+v %v", raw, result, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := tool.Fn(ctx, json.RawMessage(`{"file":"short.txt"}`))
	if err != nil || result.Err == nil {
		t.Fatalf("canceled read succeeded: %+v %v", result, err)
	}
}
