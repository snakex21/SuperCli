package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerify_FileWrite_ExistingFile_Passes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := Check{
		Family: "file_write",
		Tool:   "write_file",
		Args:   mkArgs(t, map[string]any{"path": path}),
		Result: Result{Text: "ok"},
	}
	v := DefaultVerifier{}.Verify(c)
	if !v.OK {
		t.Errorf("OK = false, reason = %q", v.Reason)
	}
}

func TestVerify_FileWrite_MissingFile_Fails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.txt")
	c := Check{
		Family: "file_write",
		Tool:   "write_file",
		Args:   mkArgs(t, map[string]any{"path": path}),
		Result: Result{Text: "ok"},
	}
	v := DefaultVerifier{}.Verify(c)
	if v.OK {
		t.Error("expected fail for missing file")
	}
	if !strings.Contains(v.Reason, "does not exist") {
		t.Errorf("reason = %q, want does not exist", v.Reason)
	}
}

func TestVerify_FileWrite_EmptyFile_Fails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := Check{
		Family: "file_write",
		Tool:   "write_file",
		Args:   mkArgs(t, map[string]any{"path": path}),
		Result: Result{Text: "ok"},
	}
	v := DefaultVerifier{}.Verify(c)
	if v.OK {
		t.Error("expected fail for empty file")
	}
	if !strings.Contains(v.Reason, "empty") {
		t.Errorf("reason = %q, want empty", v.Reason)
	}
}

func TestVerify_FileWrite_ExpectedContent_Present(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go.txt")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := Check{
		Family: "file_write",
		Tool:   "write_file",
		Args:   mkArgs(t, map[string]any{"path": path, "expected_content": "package main"}),
		Result: Result{Text: "ok"},
	}
	v := DefaultVerifier{}.Verify(c)
	if !v.OK {
		t.Errorf("OK = false, reason = %q", v.Reason)
	}
}

func TestVerify_FileWrite_ExpectedContent_Missing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go.txt")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := Check{
		Family: "file_write",
		Tool:   "write_file",
		Args:   mkArgs(t, map[string]any{"path": path, "expected_content": "WRONG"}),
		Result: Result{Text: "ok"},
	}
	v := DefaultVerifier{}.Verify(c)
	if v.OK {
		t.Error("expected fail for missing content")
	}
}

func TestVerify_Bash_EmptyOutput_Fails(t *testing.T) {
	c := Check{
		Family: "bash",
		Tool:   "run_bash",
		Args:   json.RawMessage(`{}`),
		Result: Result{Text: "  \n  "},
	}
	v := DefaultVerifier{}.Verify(c)
	if v.OK {
		t.Error("expected fail for empty bash output")
	}
}

func TestVerify_Bash_NonEmptyOutput_Passes(t *testing.T) {
	c := Check{
		Family: "bash",
		Tool:   "run_bash",
		Args:   json.RawMessage(`{}`),
		Result: Result{Text: "hello world"},
	}
	v := DefaultVerifier{}.Verify(c)
	if !v.OK {
		t.Errorf("OK = false, reason = %q", v.Reason)
	}
}

func TestVerify_Bash_ToolErrorBypassesCheck(t *testing.T) {
	c := Check{
		Family: "bash",
		Tool:   "run_bash",
		Args:   json.RawMessage(`{}`),
		Result: Result{Text: "", Err: errStr("exit 1")},
	}
	v := DefaultVerifier{}.Verify(c)
	// When the tool already errored, verification is moot
	// (the F4.d classifier handles it).
	if !v.OK {
		t.Errorf("OK = false, want pass-through on tool error")
	}
}

func TestVerify_Search_EmptyResult_Fails(t *testing.T) {
	c := Check{
		Family: "search",
		Tool:   "search_code",
		Args:   json.RawMessage(`{"query":"x"}`),
		Result: Result{Text: ""},
	}
	v := DefaultVerifier{}.Verify(c)
	if v.OK {
		t.Error("expected fail for empty search")
	}
}

func TestVerify_Search_NoResultsPrefix_Fails(t *testing.T) {
	c := Check{
		Family: "search",
		Tool:   "search_code",
		Args:   json.RawMessage(`{"query":"x"}`),
		Result: Result{Text: "no results found"},
	}
	v := DefaultVerifier{}.Verify(c)
	if v.OK {
		t.Error("expected fail for 'no results' prefix")
	}
}

func TestVerify_Search_RealResults_Passes(t *testing.T) {
	c := Check{
		Family: "search",
		Tool:   "search_code",
		Args:   json.RawMessage(`{"query":"x"}`),
		Result: Result{Text: "main.go:5: foo bar"},
	}
	v := DefaultVerifier{}.Verify(c)
	if !v.OK {
		t.Errorf("OK = false, reason = %q", v.Reason)
	}
}

func TestVerify_Read_EmptyContent_Fails(t *testing.T) {
	c := Check{
		Family: "read",
		Tool:   "read_file",
		Args:   json.RawMessage(`{"path":"x"}`),
		Result: Result{Text: ""},
	}
	v := DefaultVerifier{}.Verify(c)
	if v.OK {
		t.Error("expected fail for empty read")
	}
}

func TestVerify_Read_ImageEmptyData_Fails(t *testing.T) {
	c := Check{
		Family: "read",
		Tool:   "read_image",
		Args:   json.RawMessage(`{"path":"x"}`),
		Result: Result{Image: &ImageContent{Data: nil}},
	}
	v := DefaultVerifier{}.Verify(c)
	if v.OK {
		t.Error("expected fail for empty image data")
	}
}

func TestVerify_Read_ImageNonEmpty_Passes(t *testing.T) {
	c := Check{
		Family: "read",
		Tool:   "read_image",
		Args:   json.RawMessage(`{"path":"x"}`),
		Result: Result{Image: &ImageContent{Data: []byte{0x89, 0x50}}},
	}
	v := DefaultVerifier{}.Verify(c)
	if !v.OK {
		t.Errorf("OK = false, reason = %q", v.Reason)
	}
}

func TestVerify_Default_NoOp_Passes(t *testing.T) {
	c := Check{
		Family: "unknown_thing",
		Tool:   "weird_tool",
		Args:   json.RawMessage(`{}`),
		Result: Result{Text: "anything"},
	}
	v := DefaultVerifier{}.Verify(c)
	if !v.OK {
		t.Errorf("OK = false on default; reason = %q", v.Reason)
	}
}

func TestVerify_InferFamilyFromName(t *testing.T) {
	cases := []struct {
		tool, want string
	}{
		{"my_write", "file_write"},
		{"file_edit", "file_write"},
		{"write_file", "file_write"},
		{"run_bash", "bash"},
		{"shell_exec", "bash"},
		{"search_code", "search"},
		{"tool_search", "search"},
		{"read_image", "read"},
		{"read_file", "read"},
		{"random_thing", ""},
	}
	for _, c := range cases {
		got := inferFamily(c.tool, nil, Result{Text: "x"})
		if got != c.want {
			t.Errorf("inferFamily(%q) = %q, want %q", c.tool, got, c.want)
		}
	}
}

func TestApplyVerification_PerToolOverride(t *testing.T) {
	c := Check{
		Family: "search",
		Tool:   "x",
		Args:   json.RawMessage(`{}`),
		Result: Result{Text: "anything"},
	}
	// Override that rejects everything.
	override := func(r Result) VerifyVerdict {
		return VerifyVerdict{OK: false, Reason: "always fail"}
	}
	out := applyVerification(c, override)
	if out.Err == nil {
		t.Error("expected Err to be set")
	}
	if !strings.Contains(out.Text, "always fail") {
		t.Errorf("Text = %q, want always-fail reason", out.Text)
	}
}

func TestApplyVerification_NoOverride_UsesDefault(t *testing.T) {
	c := Check{
		Family: "bash",
		Tool:   "x",
		Args:   json.RawMessage(`{}`),
		Result: Result{Text: ""},
	}
	out := applyVerification(c, nil)
	if out.Err == nil {
		t.Error("expected Err to be set on default verification fail")
	}
}

func TestApplyVerification_PassThrough_KeepsImage(t *testing.T) {
	img := &ImageContent{Data: []byte{1, 2, 3}}
	c := Check{
		Family: "read",
		Tool:   "x",
		Args:   json.RawMessage(`{}`),
		Result: Result{Text: "claimed success", Image: img},
	}
	override := func(r Result) VerifyVerdict {
		return VerifyVerdict{OK: false, Reason: "claimed wrongly"}
	}
	out := applyVerification(c, override)
	if out.Image != img {
		t.Error("image lost on verification failure")
	}
	if !strings.Contains(out.Text, "claimed wrongly") {
		t.Errorf("Text = %q, want reason", out.Text)
	}
}

// mkArgs encodes a map[string]any to a json.RawMessage
// with the correct escaping for paths (so tests work on
// Windows where t.TempDir() returns a path with
// backslashes).
func mkArgs(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
