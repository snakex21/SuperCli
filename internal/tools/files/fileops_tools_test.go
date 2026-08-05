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

// from=0 is the single most common read_lines failure. It has exactly one
// possible meaning — start of file — so the tool serves it instead of spending
// a turn on an error.
func TestReadLinesTool_CoercesFromZero(t *testing.T) {
	_, dir := tmpToolFile(t, "alpha\nbeta\ngamma\n")
	tool := NewReadLines(dir)
	args, _ := json.Marshal(readLinesArgs{File: "test.txt", From: 0, To: 2})
	r, err := tool.execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if r.Err != nil {
		t.Fatalf("from=0 still errors: %v", r.Err)
	}
	if !strings.Contains(r.Text, "alpha") || !strings.Contains(r.Text, "beta") {
		t.Errorf("text = %s", r.Text)
	}
}

// The coercion must not swallow a genuinely impossible range.
func TestReadLinesTool_ToBelowFromStillErrors(t *testing.T) {
	_, dir := tmpToolFile(t, "alpha\nbeta\n")
	tool := NewReadLines(dir)
	args, _ := json.Marshal(readLinesArgs{File: "test.txt", From: 0, To: 0})
	r, _ := tool.execute(context.Background(), args)
	if r.Err == nil {
		t.Fatal("expected error for to < from")
	}
	if !strings.Contains(r.Err.Error(), "1-based") {
		t.Errorf("error does not say lines are 1-based: %v", r.Err)
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

// ========== cross-tool: Verify family inference ==========

func TestF24_Tools_VerifyFamilies(t *testing.T) {
	// read_lines and read_context should be "read" family;
	// patch_file and create_file should be "file_write"
	tests := []struct {
		name   string
		tool   Tool
		expect string
	}{
		{"read_lines", NewReadLines(".").Spec(), "read"},
		{"read_context", NewReadContext(".").Spec(), "read"},
		{"patch_file", NewPatchFile(".").Spec(), "file_write"},
		{"create_file", NewCreateFile(".").Spec(), "file_write"},
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

// TestEditTools_SandboxEscape verifies the F-thin security fix: the edit
// tools must reject paths that escape the project home (absolute paths and ..
// traversal), just like write_file/move/copy/trash. Before the fix their
// resolvePath accepted any absolute path, letting the model edit outside home.
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
		{"patch_file", func(p string) Result {
			args, _ := json.Marshal(patchFileArgs{Path: p, Old: "original", New: "HACKED"})
			r, _ := NewPatchFile(dir).execute(context.Background(), args)
			return r
		}},
		{"patch_file_changes", func(p string) Result {
			args, _ := json.Marshal(patchFileArgs{Path: p, Changes: []patchFileChange{{Old: "original", New: "HACKED"}}})
			r, _ := NewPatchFile(dir).execute(context.Background(), args)
			return r
		}},
		{"create_file", func(p string) Result {
			args, _ := json.Marshal(map[string]string{"path": p, "content": "HACKED"})
			r, _ := NewCreateFile(dir).execute(context.Background(), args)
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
