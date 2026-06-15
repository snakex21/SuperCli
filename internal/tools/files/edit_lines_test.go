package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditLinesTool_MultiApply(t *testing.T) {
	_, dir := tmpToolFile(t, "aaa\nbbb\nccc\nddd\n")
	tool := NewEditLines(dir)
	args, _ := json.Marshal(editLinesArgs{
		File: "test.txt",
		Edits: []editLinesEdit{
			{Line: 1, ExpectedOld: "aaa", NewContent: "AAA"},
			{Line: 3, ExpectedOld: "ccc", NewContent: "CCC"},
		},
	})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if r.Err != nil {
		t.Fatalf("unexpected tool error: %v", r.Err)
	}
	if !strings.Contains(r.Text, "Edited 2 lines") {
		t.Errorf("text = %s", r.Text)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if lines[0] != "AAA" || lines[1] != "bbb" || lines[2] != "CCC" || lines[3] != "ddd" {
		t.Errorf("lines = %v", lines)
	}
}

func TestEditLinesTool_MismatchFailsLoudNoWrite(t *testing.T) {
	_, dir := tmpToolFile(t, "aaa\nbbb\nccc\n")
	tool := NewEditLines(dir)
	args, _ := json.Marshal(editLinesArgs{
		File: "test.txt",
		Edits: []editLinesEdit{
			{Line: 1, ExpectedOld: "aaa", NewContent: "AAA"},
			{Line: 2, ExpectedOld: "nope", NewContent: "X"},
		},
	})
	r, _ := tool.execute(context.Background(), args)
	if r.Err == nil {
		t.Fatal("expected error for content mismatch")
	}
	// Atomic: the valid edit must not have landed either.
	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	if strings.Contains(string(data), "AAA") || strings.Contains(string(data), "X") {
		t.Errorf("file mutated on mismatch: %q", string(data))
	}
}

func TestEditLinesTool_StaleLineNumbers(t *testing.T) {
	_, dir := tmpToolFile(t, "one\ntwo\nthree\n")
	tool := NewEditLines(dir)
	// Hints off by +10; content still anchors.
	args, _ := json.Marshal(editLinesArgs{
		File: "test.txt",
		Edits: []editLinesEdit{
			{Line: 11, ExpectedOld: "one", NewContent: "ONE"},
			{Line: 13, ExpectedOld: "three", NewContent: "THREE"},
		},
	})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if r.Err != nil {
		t.Fatalf("unexpected tool error: %v", r.Err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if lines[0] != "ONE" || lines[1] != "two" || lines[2] != "THREE" {
		t.Errorf("lines = %v", lines)
	}
}

func TestEditLinesTool_EmptyEdits(t *testing.T) {
	_, dir := tmpToolFile(t, "a\n")
	tool := NewEditLines(dir)
	args, _ := json.Marshal(editLinesArgs{File: "test.txt"})
	r, _ := tool.execute(context.Background(), args)
	if r.Err == nil {
		t.Error("expected error for empty edits")
	}
}

func TestEditLinesTool_MissingAnchor(t *testing.T) {
	_, dir := tmpToolFile(t, "a\nb\n")
	tool := NewEditLines(dir)
	args, _ := json.Marshal(editLinesArgs{
		File:  "test.txt",
		Edits: []editLinesEdit{{Line: 1, NewContent: "X"}}, // no expected_old
	})
	r, _ := tool.execute(context.Background(), args)
	if r.Err == nil {
		t.Fatal("expected error for missing expected_old")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	if strings.Contains(string(data), "X") {
		t.Errorf("file mutated: %q", string(data))
	}
}

func TestEditLinesTool_BadJSON(t *testing.T) {
	tool := NewEditLines(".")
	r, _ := tool.execute(context.Background(), []byte("bad"))
	if r.Err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestEditLinesTool_Spec(t *testing.T) {
	spec := NewEditLines(".").Spec()
	if spec.Name != "edit_lines" {
		t.Errorf("Name = %q", spec.Name)
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
	if !strings.Contains(spec.Schema, "expected_old") {
		t.Errorf("schema missing expected_old: %s", spec.Schema)
	}
}
