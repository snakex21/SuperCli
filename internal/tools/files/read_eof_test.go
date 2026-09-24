package files

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools/fileops"
)

func TestReadToolsReportObservedEOF(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "tiny.go")
	for _, text := range []string{"one\ntwo", "one\ntwo\n", "one\r\ntwo\r\n"} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name, args string
			tool       Tool
		}{
			{"exact", "{\"file\":\"tiny.go\",\"from\":1,\"to\":2}", NewReadLines(root).Spec()},
			{"past", "{\"file\":\"tiny.go\",\"from\":1,\"to\":20}", NewReadLines(root).Spec()},
			{"context", "{\"file\":\"tiny.go\",\"line\":1,\"radius\":1}", NewReadContext(root).Spec()},
			{"batch", "{\"reads\":\"tiny.go:1-2 | tiny.go:1-1\"}", NewReadMany(root).Spec()},
		} {
			result, err := tc.tool.Fn(context.Background(), json.RawMessage(tc.args))
			if err != nil || result.Err != nil || strings.Count(result.Text, "[end of file at line 2]") != 1 {
				t.Fatalf("%s %q: result=%+v err=%v", tc.name, text, result, err)
			}
		}
	}
	// A new read sees an appended file, rather than reusing old EOF metadata.
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := NewReadLines(root).Spec().Fn(context.Background(), json.RawMessage("{\"file\":\"tiny.go\",\"from\":1,\"to\":2}"))
	if err != nil || got.Err != nil || strings.Contains(got.Text, "end of file") {
		t.Fatalf("stale EOF: %+v %v", got, err)
	}
	if got.Text != "   1 | one\n   2 | two\n" {
		t.Fatalf("partial range changed: %q", got.Text)
	}
}

func TestReadEOFDoesNotHideOmissionsOrErrors(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"empty.txt", "long.txt"} {
		data := ""
		if name == "long.txt" {
			data = strings.Repeat(strings.Repeat("x", 1900)+"\n", 100)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	tool := NewReadLines(root).Spec()
	got, err := tool.Fn(context.Background(), json.RawMessage("{\"file\":\"long.txt\",\"from\":1,\"to\":100}"))
	if err != nil || got.Err != nil || !strings.Contains(got.Text, "more line(s) not shown") || strings.Contains(got.Text, "end of file") {
		t.Fatalf("truncated view: %+v %v", got, err)
	}
	for _, args := range []string{"{\"file\":\"empty.txt\",\"from\":1,\"to\":2}", "{\"file\":\"long.txt\",\"from\":101,\"to\":110}"} {
		result, err := tool.Fn(context.Background(), json.RawMessage(args))
		if err != nil || result.Err == nil || strings.Contains(result.Text, "end of file") {
			t.Fatalf("error changed: %+v %v", result, err)
		}
	}
	longLine := renderLinesWithEOF([]fileops.LineRange{{Number: 1, Content: strings.Repeat("x", 3000)}}, true)
	if !strings.Contains(longLine, "truncated") || !strings.Contains(longLine, "[end of file at line 1]") {
		t.Fatalf("long-line omission lost: %s", longLine)
	}
}

func BenchmarkReadEOF(b *testing.B) {
	root := b.TempDir()
	for _, count := range []int{20, 20000} {
		file := fmt.Sprintf("file-%d.txt", count)
		if err := os.WriteFile(filepath.Join(root, file), []byte(strings.Repeat("value=42\n", count)), 0600); err != nil {
			b.Fatal(err)
		}
		args := json.RawMessage(fmt.Sprintf("{\"file\":%q,\"from\":1,\"to\":20}", file))
		tool := NewReadLines(root).Spec()
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				got, err := tool.Fn(context.Background(), args)
				if err != nil || got.Err != nil {
					b.Fatalf("%+v %v", got, err)
				}
			}
		})
	}
}
