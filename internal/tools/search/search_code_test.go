package search

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchCode_Spec(t *testing.T) {
	s := NewSearchCode(".")
	spec := s.Spec()
	if spec.Name != "search_code" {
		t.Errorf("Name = %q", spec.Name)
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
	if !strings.Contains(spec.Schema, "query") {
		t.Errorf("schema missing query: %q", spec.Schema)
	}
}

func TestSearchCode_RejectsEmptyQuery(t *testing.T) {
	s := NewSearchCode(t.TempDir())
	res, _ := s.run(context.Background(), json.RawMessage(`{"query":""}`))
	if res.Err == nil {
		t.Fatal("expected error on empty query")
	}
}

func TestSearchCode_BadArgs(t *testing.T) {
	s := NewSearchCode(t.TempDir())
	res, _ := s.run(context.Background(), json.RawMessage("not json"))
	if res.Err == nil {
		t.Fatal("expected error on bad args")
	}
}

func TestSearchCode_FindsMatchInFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("line one\nfind me here\nline three"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewSearchCode(dir)
	res, _ := s.run(context.Background(), json.RawMessage(`{"query":"find me"}`))
	if res.Err != nil {
		t.Fatalf("run: %v", res.Err)
	}
	if !strings.Contains(res.Text, "find me here") {
		t.Errorf("Text = %q", res.Text)
	}
	if !strings.Contains(res.Text, "a.txt:2") {
		t.Errorf("Text missing file:line: %q", res.Text)
	}
}

func TestSearchCode_NoMatches(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644)
	s := NewSearchCode(dir)
	res, _ := s.run(context.Background(), json.RawMessage(`{"query":"nothere"}`))
	if res.Err != nil {
		t.Fatalf("run: %v", res.Err)
	}
	if !strings.Contains(res.Text, "no matches") {
		t.Errorf("Text = %q", res.Text)
	}
}

func TestSearchCode_SkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755)
	os.WriteFile(filepath.Join(dir, "node_modules", "bad.txt"), []byte("find me"), 0o644)
	os.WriteFile(filepath.Join(dir, "good.txt"), []byte("find me too"), 0o644)
	s := NewSearchCode(dir)
	res, _ := s.run(context.Background(), json.RawMessage(`{"query":"find me"}`))
	if res.Text == "no matches" {
		t.Fatal("expected to find match in good.txt")
	}
	if strings.Contains(res.Text, "node_modules") {
		t.Errorf("Text should not include node_modules: %q", res.Text)
	}
}

func TestSearchCode_MaxLimit(t *testing.T) {
	dir := t.TempDir()
	// Create 5 files with the same marker.
	for i := 0; i < 5; i++ {
		name := filepath.Join(dir, "f"+string(rune('0'+i))+".txt")
		os.WriteFile(name, []byte("match"), 0o644)
	}
	s := NewSearchCode(dir)
	res, _ := s.run(context.Background(), json.RawMessage(`{"query":"match","max":2}`))
	if res.Err != nil {
		t.Fatalf("run: %v", res.Err)
	}
	lines := strings.Split(strings.TrimSpace(res.Text), "\n")
	if len(lines) != 2 {
		t.Errorf("got %d lines, want 2", len(lines))
	}
}

func TestMatchLine(t *testing.T) {
	if !matchLine("Hello World", "world") {
		t.Error("case-insensitive match failed")
	}
	if matchLine("Hello", "World!") {
		t.Error("non-match returned true")
	}
}

func TestSearchRootDirs(t *testing.T) {
	dirs := searchRootDirs("")
	if len(dirs) != 1 {
		t.Errorf("len = %d", len(dirs))
	}
}
