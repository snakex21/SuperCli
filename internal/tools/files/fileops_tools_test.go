package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper: write a temp file and return its path + base dir.
func tmpToolFile(t *testing.T, content string) (basePath, baseDir string) {
	t.Helper()
	dir := t.TempDir()
	basePath = filepath.Join(dir, "test.txt")
	if err := os.WriteFile(basePath, []byte(content), 0644); err != nil {
		t.Fatalf("tmpToolFile: %v", err)
	}
	return basePath, dir
}

// ========== read_lines ==========

func TestReadLinesTool_Basic(t *testing.T) {
	path, dir := tmpToolFile(t, "alpha\nbeta\ngamma\n")
	tool := NewReadLines(dir)
	args, _ := json.Marshal(readLinesArgs{File: "test.txt", From: 1, To: 2})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(r.Text, "alpha") || !strings.Contains(r.Text, "beta") {
		t.Errorf("text missing expected lines: %s", r.Text)
	}
	_ = path
}

func TestReadLinesTool_AbsolutePath(t *testing.T) {
	path, dir := tmpToolFile(t, "line1\nline2\n")
	tool := NewReadLines(dir)
	args, _ := json.Marshal(readLinesArgs{File: path, From: 1, To: 1})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(r.Text, "line1") {
		t.Errorf("text = %s", r.Text)
	}
}

func TestReadLinesTool_BadJSON(t *testing.T) {
	tool := NewReadLines(".")
	r, _ := tool.execute(context.Background(), []byte("bad"))
	if r.Err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestReadLinesTool_FileNotFound(t *testing.T) {
	tool := NewReadLines(t.TempDir())
	args, _ := json.Marshal(readLinesArgs{File: "nonexistent.txt", From: 1, To: 5})
	r, _ := tool.execute(context.Background(), args)
	if r.Err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadLinesTool_Spec(t *testing.T) {
	tool := NewReadLines(".")
	spec := tool.Spec()
	if spec.Name != "read_lines" {
		t.Errorf("Name = %q", spec.Name)
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
}

// ========== read_context ==========

func TestReadContextTool_Basic(t *testing.T) {
	_, dir := tmpToolFile(t, "l1\nl2\nl3\nl4\nl5\n")
	tool := NewReadContext(dir)
	args, _ := json.Marshal(readContextArgs{File: "test.txt", Line: 3, Radius: 1})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(r.Text, "l2") || !strings.Contains(r.Text, "l3") || !strings.Contains(r.Text, "l4") {
		t.Errorf("text missing context lines: %s", r.Text)
	}
}

func TestReadContextTool_DefaultRadius(t *testing.T) {
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = "line"
	}
	_, dir := tmpToolFile(t, strings.Join(lines, "\n")+"\n")
	tool := NewReadContext(dir)
	args, _ := json.Marshal(readContextArgs{File: "test.txt", Line: 15, Radius: 0})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Should have many lines (default radius 10)
	if strings.Count(r.Text, "line") < 10 {
		t.Errorf("too few lines in context: %s", r.Text)
	}
}

func TestReadContextTool_BadJSON(t *testing.T) {
	tool := NewReadContext(".")
	r, _ := tool.execute(context.Background(), []byte("{bad"))
	if r.Err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestReadContextTool_Spec(t *testing.T) {
	tool := NewReadContext(".")
	spec := tool.Spec()
	if spec.Name != "read_context" {
		t.Errorf("Name = %q", spec.Name)
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
}

// ========== edit_line ==========

func TestEditLineTool_Basic(t *testing.T) {
	_, dir := tmpToolFile(t, "aaa\nbbb\nccc\n")
	tool := NewEditLine(dir)
	args, _ := json.Marshal(editLineArgs{File: "test.txt", Line: 2, NewContent: "BBB"})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(r.Text, "Edited line 2") {
		t.Errorf("text = %s", r.Text)
	}
	if !strings.Contains(r.Text, "BBB") {
		t.Errorf("text missing new content: %s", r.Text)
	}
	// Verify the file was actually changed.
	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	if !strings.Contains(string(data), "BBB") {
		t.Error("file not actually changed")
	}
}

func TestEditLineTool_BadJSON(t *testing.T) {
	tool := NewEditLine(".")
	r, _ := tool.execute(context.Background(), []byte("bad"))
	if r.Err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestEditLineTool_LineExceedsLength(t *testing.T) {
	_, dir := tmpToolFile(t, "a\n")
	tool := NewEditLine(dir)
	args, _ := json.Marshal(editLineArgs{File: "test.txt", Line: 10, NewContent: "x"})
	r, _ := tool.execute(context.Background(), args)
	if r.Err == nil {
		t.Error("expected error for line > file length")
	}
}

func TestEditLineTool_Spec(t *testing.T) {
	tool := NewEditLine(".")
	spec := tool.Spec()
	if spec.Name != "edit_line" {
		t.Errorf("Name = %q", spec.Name)
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
}

func TestEditLineTool_Anchored_DriftCorrected(t *testing.T) {
	// Content "bbb" is at line 2; the model hints line 4.
	// The anchored path must find it by content and edit it.
	_, dir := tmpToolFile(t, "aaa\nbbb\nccc\nddd\n")
	tool := NewEditLine(dir)
	args, _ := json.Marshal(editLineArgs{
		File: "test.txt", Line: 4, NewContent: "BBB", ExpectedOld: "bbb",
	})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if r.Err != nil {
		t.Fatalf("unexpected tool error: %v", r.Err)
	}
	if !strings.Contains(r.Text, "anchored") {
		t.Errorf("text should mark anchored edit: %s", r.Text)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if lines[1] != "BBB" {
		t.Errorf("line 2 = %q, want BBB", lines[1])
	}
	if lines[2] != "ccc" {
		t.Errorf("line 3 mutated: %q", lines[2])
	}
}

func TestEditLineTool_Anchored_MismatchFailsLoud(t *testing.T) {
	// expected_old does not exist → must fail, file untouched.
	_, dir := tmpToolFile(t, "aaa\nbbb\nccc\n")
	tool := NewEditLine(dir)
	args, _ := json.Marshal(editLineArgs{
		File: "test.txt", Line: 2, NewContent: "X", ExpectedOld: "not-here",
	})
	r, _ := tool.execute(context.Background(), args)
	if r.Err == nil {
		t.Fatal("expected error for content mismatch")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	if !strings.Contains(string(data), "bbb") || strings.Contains(string(data), "X") {
		t.Errorf("file mutated on anchored mismatch: %q", string(data))
	}
}

func TestEditLineTool_Anchored_SchemaMentionsExpectedOld(t *testing.T) {
	spec := NewEditLine(".").Spec()
	if !strings.Contains(spec.Schema, "expected_old") {
		t.Errorf("schema missing expected_old: %s", spec.Schema)
	}
}

// ========== insert_after ==========

func TestInsertAfterTool_Basic(t *testing.T) {
	_, dir := tmpToolFile(t, "aaa\nbbb\n")
	tool := NewInsertAfter(dir)
	args, _ := json.Marshal(insertAfterArgs{File: "test.txt", Line: 1, Content: "INSERTED"})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(r.Text, "Inserted after line 1") {
		t.Errorf("text = %s", r.Text)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Errorf("want 3 lines, got %d: %v", len(lines), lines)
	}
	if lines[1] != "INSERTED" {
		t.Errorf("line 2 = %q, want INSERTED", lines[1])
	}
}

func TestInsertAfterTool_BadJSON(t *testing.T) {
	tool := NewInsertAfter(".")
	r, _ := tool.execute(context.Background(), []byte("{bad"))
	if r.Err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestInsertAfterTool_Spec(t *testing.T) {
	tool := NewInsertAfter(".")
	spec := tool.Spec()
	if spec.Name != "insert_after" {
		t.Errorf("Name = %q", spec.Name)
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
}

// ========== delete_lines ==========

func TestDeleteLinesTool_Basic(t *testing.T) {
	_, dir := tmpToolFile(t, "aaa\nbbb\nccc\n")
	tool := NewDeleteLines(dir)
	args, _ := json.Marshal(deleteLinesArgs{File: "test.txt", From: 2, To: 2})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(r.Text, "Deleted lines 2-2") {
		t.Errorf("text = %s", r.Text)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("want 2 lines, got %d", len(lines))
	}
}

func TestDeleteLinesTool_Range(t *testing.T) {
	_, dir := tmpToolFile(t, "a\nb\nc\nd\ne\n")
	tool := NewDeleteLines(dir)
	args, _ := json.Marshal(deleteLinesArgs{File: "test.txt", From: 2, To: 4})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(r.Text, "Deleted lines 2-4") {
		t.Errorf("text = %s", r.Text)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 || lines[0] != "a" || lines[1] != "e" {
		t.Errorf("remaining: %v, want [a e]", lines)
	}
}

func TestDeleteLinesTool_BadJSON(t *testing.T) {
	tool := NewDeleteLines(".")
	r, _ := tool.execute(context.Background(), []byte("{bad"))
	if r.Err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestDeleteLinesTool_Spec(t *testing.T) {
	tool := NewDeleteLines(".")
	spec := tool.Spec()
	if spec.Name != "delete_lines" {
		t.Errorf("Name = %q", spec.Name)
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
}

// ========== cross-tool: Verify family inference ==========

func TestF24_Tools_VerifyFamilies(t *testing.T) {
	// read_lines and read_context should be "read" family
	// edit_line, insert_after, delete_lines should be "file_write"
	tests := []struct {
		name   string
		tool   Tool
		expect string
	}{
		{"read_lines", NewReadLines(".").Spec(), "read"},
		{"read_context", NewReadContext(".").Spec(), "read"},
		{"edit_line", NewEditLine(".").Spec(), "file_write"},
		{"insert_after", NewInsertAfter(".").Spec(), "file_write"},
		{"delete_lines", NewDeleteLines(".").Spec(), "file_write"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The DefaultVerifier uses inferFamily which
			// checks name prefixes. Verify the name matches
			// the expected pattern.
			switch tt.expect {
			case "read":
				if !strings.HasPrefix(tt.tool.Name, "read_") {
					t.Errorf("read family tool %q should start with read_", tt.tool.Name)
				}
			case "file_write":
				if strings.HasPrefix(tt.tool.Name, "read_") {
					t.Errorf("write family tool %q should NOT start with read_", tt.tool.Name)
				}
			}
		})
	}
}

// TestEditTools_SandboxEscape verifies the F-thin security fix:
// edit_line, insert_after and delete_lines must reject paths that
// escape the project home (absolute paths and .. traversal), just
// like write_file/move/copy/trash. Before the fix their resolvePath
// accepted any absolute path, letting the model edit outside home.
func TestEditTools_SandboxEscape(t *testing.T) {
	dir := t.TempDir()
	// A real file OUTSIDE home that must never be touched.
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		run  func(rel string) Result
	}{
		{"edit_line", func(p string) Result {
			args, _ := json.Marshal(editLineArgs{File: p, Line: 1, NewContent: "HACKED"})
			r, _ := NewEditLine(dir).execute(context.Background(), args)
			return r
		}},
		{"insert_after", func(p string) Result {
			args, _ := json.Marshal(insertAfterArgs{File: p, Line: 1, Content: "HACKED"})
			r, _ := NewInsertAfter(dir).execute(context.Background(), args)
			return r
		}},
		{"delete_lines", func(p string) Result {
			args, _ := json.Marshal(deleteLinesArgs{File: p, From: 1, To: 1})
			r, _ := NewDeleteLines(dir).execute(context.Background(), args)
			return r
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name+"_absolute", func(t *testing.T) {
			r := tc.run(outside)
			if r.Err == nil {
				t.Errorf("%s accepted an absolute path outside home", tc.name)
			}
			if data, _ := os.ReadFile(outside); string(data) != "original" {
				t.Errorf("%s modified a file outside home!", tc.name)
			}
		})
		t.Run(tc.name+"_traversal", func(t *testing.T) {
			r := tc.run("../../../etc/hosts")
			if r.Err == nil {
				t.Errorf("%s accepted a .. traversal path", tc.name)
			}
		})
	}
}
