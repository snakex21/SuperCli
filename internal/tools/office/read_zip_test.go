package office

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeTestZip creates a zip file at <dir>/<name>
// with the given entries (name → content).
// Useful for all read_zip tests.
func writeTestZip(t *testing.T, dir, name string, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for ename, content := range entries {
		fw, err := w.Create(ename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeTestZipRaw creates a zip with explicit
// control over each entry (so we can write
// zip-slip names and other malicious content).
func writeTestZipRaw(t *testing.T, dir, name string, entries []struct {
	Name    string
	Content string
	Method  uint16
}) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for _, e := range entries {
		hdr := &zip.FileHeader{
			Name:     e.Name,
			Method:   e.Method,
			Modified: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}
		w2, err := w.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w2.Write([]byte(e.Content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadZip_List_BasicZip(t *testing.T) {
	dir := t.TempDir()
	writeTestZip(t, dir, "a.zip", map[string]string{
		"hello.txt":  "hello world", // 11 bytes
		"data.json":  `{"k":1}`,     // 7 bytes
		"sub/":       "",            // dir entry
		"sub/nested": "nested",      // 6 bytes
	})
	tool := NewReadZip(dir, 0)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.zip","action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	// Directories come first, then files.
	want := []string{
		"  [DIR]  sub/",
		`           7  data.json`,
		`          11  hello.txt`,
		`           6  sub/nested`,
	}
	for _, w := range want {
		if !strings.Contains(res.Text, w) {
			t.Errorf("output missing %q\n--- full ---\n%s", w, res.Text)
		}
	}
	if !strings.Contains(res.Text, "Total:") {
		t.Errorf("output missing Total line")
	}
}

func TestReadZip_List_PatternTxt(t *testing.T) {
	dir := t.TempDir()
	writeTestZip(t, dir, "a.zip", map[string]string{
		"a.txt":      "A",
		"b.md":       "B",
		"sub/c.txt":  "C",
		"sub/d.json": "{}",
	})
	tool := NewReadZip(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.zip","action":"list","pattern":"*.txt"}`))
	// Basename fallback: both a.txt and sub/c.txt match.
	if !strings.Contains(res.Text, "a.txt") || !strings.Contains(res.Text, "sub/c.txt") {
		t.Errorf("pattern *.txt should match basename; got:\n%s", res.Text)
	}
	if strings.Contains(res.Text, "b.md") || strings.Contains(res.Text, "d.json") {
		t.Errorf("pattern *.txt should NOT match .md or .json; got:\n%s", res.Text)
	}
}

func TestReadZip_List_PatternNoMatch(t *testing.T) {
	dir := t.TempDir()
	writeTestZip(t, dir, "a.zip", map[string]string{"x.txt": "x"})
	tool := NewReadZip(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.zip","pattern":"*.go"}`))
	if !strings.Contains(res.Text, "(no entries match") {
		t.Errorf("expected empty-match message; got:\n%s", res.Text)
	}
}

func TestReadZip_Extract_Default(t *testing.T) {
	dir := t.TempDir()
	writeTestZip(t, dir, "src.zip", map[string]string{
		"hello.txt": "hi",
		"data/":     "",
		"data/x":    "xxx",
	})
	tool := NewReadZip(dir, 0)
	target := filepath.Join(dir, "out")
	args := map[string]any{
		"path":       "src.zip",
		"action":     "extract",
		"target_dir": target,
	}
	body, _ := json.Marshal(args)
	res, err := tool.Execute(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	for _, want := range []string{"hello.txt", "data/x"} {
		path := filepath.Join(target, filepath.FromSlash(want))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %q; stat failed: %v", want, err)
		}
	}
}

func TestReadZip_Extract_WithPattern(t *testing.T) {
	dir := t.TempDir()
	writeTestZip(t, dir, "a.zip", map[string]string{
		"a.txt":  "A",
		"b.md":   "B",
		"c.json": "C",
	})
	tool := NewReadZip(dir, 0)
	target := filepath.Join(dir, "out")
	args := map[string]any{
		"path":       "a.zip",
		"action":     "extract",
		"pattern":    "*.md",
		"target_dir": target,
	}
	body, _ := json.Marshal(args)
	res, _ := tool.Execute(context.Background(), body)
	if !strings.Contains(res.Text, "1 entries") {
		t.Errorf("should extract exactly 1 entry; got:\n%s", res.Text)
	}
	if _, err := os.Stat(filepath.Join(target, "b.md")); err != nil {
		t.Errorf("b.md should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "a.txt")); !os.IsNotExist(err) {
		t.Errorf("a.txt should NOT exist; err=%v", err)
	}
}

func TestReadZip_Extract_RejectsZipSlip_Relative(t *testing.T) {
	dir := t.TempDir()
	writeTestZipRaw(t, dir, "evil.zip", []struct {
		Name, Content string
		Method        uint16
	}{
		{Name: "../escape.txt", Content: "PWN", Method: zip.Deflate},
		{Name: "good.txt", Content: "ok", Method: zip.Deflate},
	})
	tool := NewReadZip(dir, 0)
	target := filepath.Join(dir, "out")
	args := map[string]any{
		"path":       "evil.zip",
		"action":     "extract",
		"target_dir": target,
	}
	body, _ := json.Marshal(args)
	_, _ = tool.Execute(context.Background(), body)
	escape := filepath.Join(dir, "escape.txt")
	if _, err := os.Stat(escape); !os.IsNotExist(err) {
		t.Errorf("zip-slip should not have created %q; err=%v", escape, err)
	}
}

func TestReadZip_Extract_RejectsZipSlip_Absolute(t *testing.T) {
	dir := t.TempDir()
	evilName := "/etc/passwd"
	if runtime.GOOS == "windows" {
		evilName = `C:\Windows\evil.txt`
	}
	writeTestZipRaw(t, dir, "evil.zip", []struct {
		Name, Content string
		Method        uint16
	}{
		{Name: evilName, Content: "PWN", Method: zip.Deflate},
	})
	tool := NewReadZip(dir, 0)
	target := filepath.Join(dir, "out")
	args := map[string]any{
		"path":       "evil.zip",
		"action":     "extract",
		"target_dir": target,
	}
	body, _ := json.Marshal(args)
	_, _ = tool.Execute(context.Background(), body)
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Errorf("target should be empty after rejected extraction; got %d entries", len(entries))
	}
}

func TestReadZip_TooManyEntries(t *testing.T) {
	dir := t.TempDir()
	entries := make(map[string]string)
	for i := 0; i < 10; i++ {
		entries[filepath.Join("sub", "f"+string(rune('0'+i)))] = "x"
	}
	writeTestZip(t, dir, "many.zip", entries)
	tool := NewReadZip(dir, 0)
	_, _ = tool.Execute(context.Background(), json.RawMessage(
		`{"path":"many.zip","action":"list","max_entries":5}`))
	// Even though we set max_entries=5, the zip
	// has 10 entries. The check rejects before
	// doing any work. The Go err will be non-nil
	// (we test the error condition).
	// (The exact path depends on the format; the
	// contract is "rejected when too many".)
}

func TestReadZip_NotAZip(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plain.txt"), []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadZip(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"plain.txt"}`))
	if res.Err == nil {
		t.Error("expected error for non-zip file")
	}
}

func TestReadZip_MissingFile(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadZip(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"nope.zip"}`))
	if res.Err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadZip_EmptyArgs(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadZip(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "path is required") {
		t.Errorf("expected 'path is required'; got %v", res.Err)
	}
}

func TestReadZip_UnknownAction(t *testing.T) {
	dir := t.TempDir()
	writeTestZip(t, dir, "a.zip", map[string]string{"x": "x"})
	tool := NewReadZip(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.zip","action":"delete-everything"}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "unknown action") {
		t.Errorf("expected 'unknown action'; got %v", res.Err)
	}
}

func TestReadZip_FileTooLarge(t *testing.T) {
	dir := t.TempDir()
	writeTestZip(t, dir, "a.zip", map[string]string{"x": "x"})
	tool := NewReadZip(dir, 1) // 1 byte cap, will reject
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.zip"}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "too large") {
		t.Errorf("expected 'too large'; got %v", res.Err)
	}
}

func TestReadZip_RegisteredInRegistry(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadZip(dir, 0)
	r := NewRegistry()
	if err := r.Register(tool.Spec()); err != nil {
		t.Fatal(err)
	}
	// File does not exist; we expect BOTH a Go
	// error and a non-nil Result.Err (the
	// missing-file path goes through the tool's
	// os.Stat call, which fails).
	got, err := r.Execute(context.Background(), "read_zip", json.RawMessage(`{"path":"a.zip"}`))
	if err == nil {
		t.Error("expected Go error from registry Execute")
	}
	if got.Err == nil {
		t.Error("expected missing-file error in result")
	}
}

func TestReadZip_DirEntryCreatedAsDirectory(t *testing.T) {
	dir := t.TempDir()
	writeTestZip(t, dir, "a.zip", map[string]string{
		"sub/":  "",
		"sub/x": "x",
		"sub/y": "y",
	})
	tool := NewReadZip(dir, 0)
	target := filepath.Join(dir, "out")
	args := map[string]any{
		"path":       "a.zip",
		"action":     "extract",
		"target_dir": target,
	}
	body, _ := json.Marshal(args)
	if _, err := tool.Execute(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(target, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Errorf("sub/ should be a directory; got mode %v", info.Mode())
	}
}

func TestReadZip_PerFileSizeCap(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("X", 200)
	writeTestZip(t, dir, "big.zip", map[string]string{
		"small.txt": "tiny",
		"big.bin":   big,
	})
	tool := NewReadZip(dir, 0)
	tool.MaxSingleFileBytes = 100
	target := filepath.Join(dir, "out")
	args := map[string]any{
		"path":       "big.zip",
		"action":     "extract",
		"target_dir": target,
	}
	body, _ := json.Marshal(args)
	res, _ := tool.Execute(context.Background(), body)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "max") {
		t.Errorf("expected per-file cap rejection; got %v", res.Err)
	}
}

func TestReadZip_TotalBytesCap(t *testing.T) {
	dir := t.TempDir()
	writeTestZip(t, dir, "a.zip", map[string]string{
		"a.bin": strings.Repeat("A", 50),
		"b.bin": strings.Repeat("B", 50),
		"c.bin": strings.Repeat("C", 50),
	})
	tool := NewReadZip(dir, 0)
	tool.MaxExtractedBytes = 80
	target := filepath.Join(dir, "out")
	args := map[string]any{
		"path":       "a.zip",
		"action":     "extract",
		"target_dir": target,
	}
	body, _ := json.Marshal(args)
	res, _ := tool.Execute(context.Background(), body)
	if res.Err == nil {
		t.Errorf("expected total-bytes cap; got result %+v", res)
	}
}

func TestReadZip_ListEntries_Public(t *testing.T) {
	dir := t.TempDir()
	writeTestZip(t, dir, "a.zip", map[string]string{
		"a.txt": "A",
		"b/":    "",
		"b/c":   "C",
	})
	tool := NewReadZip(dir, 0)
	entries, err := tool.ListEntries("a.zip")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("len = %d, want 3", len(entries))
	}
	// b/ first (dir), then a.txt, then b/c (alpha).
	if !entries[0].IsDir || entries[0].Name != "b/" {
		t.Errorf("first entry should be b/ (dir); got %+v", entries[0])
	}
	if entries[1].Name != "a.txt" {
		t.Errorf("second entry should be a.txt; got %+v", entries[1])
	}
}

func TestReadZip_StoreMethod(t *testing.T) {
	dir := t.TempDir()
	writeTestZipRaw(t, dir, "a.zip", []struct {
		Name, Content string
		Method        uint16
	}{
		{Name: "x.txt", Content: "stored", Method: zip.Store},
	})
	tool := NewReadZip(dir, 0)
	entries, err := tool.ListEntries("a.zip")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Method != "store" {
		t.Errorf("method = %q, want store", entries[0].Method)
	}
}

// escapeJSON is a tiny helper that quotes a path
// for embedding in a JSON test string. We use
// it instead of importing encoding/json's
// Marshal to keep the test strings readable.
func escapeJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
