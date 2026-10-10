package office

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools/core"
)

func TestReadZip_ReadFindsEntryInSecondExplicitPart(t *testing.T) {
	dir := t.TempDir()
	writeTestZip(t, dir, "part01.zip", map[string]string{
		"Bundle/MANIFEST.json": `{"parts":2}`,
	})
	text := "{\"fixture\":true,\"models\":2}\n"
	entry := "Bundle/pliki/assets/travelers_v3_manifest.json"
	writeTestZip(t, dir, "part02.zip", map[string]string{entry: text})
	tool := NewReadZip(dir, 0)
	reg := core.NewRegistry()
	reg.MustRegister(tool.Spec())
	// Reproduce the saved session's wrong-part lookup, then locate and read
	// the entry in one call over the two explicitly supplied parts.
	miss, err := reg.Execute(context.Background(), "read_zip", json.RawMessage(`{"path":"part01.zip","action":"read","pattern":"travelers_v3_manifest.json"}`))
	if err != nil || miss.Err != nil || !strings.Contains(miss.Text, "no entries match") {
		t.Fatalf("first part: result=%+v err=%v", miss, err)
	}
	res, err := reg.Execute(context.Background(), "read_zip", json.RawMessage(`{"paths":["part01.zip","part02.zip"],"action":"read","pattern":"travelers_v3_manifest.json"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("explicit parts: result=%+v err=%v", res, err)
	}
	if !strings.Contains(res.Text, `archive="part02.zip" entry="`+entry+`"`) || !strings.Contains(res.Text, text) {
		t.Fatalf("second-part identity or exact text missing: %q", res.Text)
	}
	if strings.Contains(res.Text, `archive="part01.zip"`) || strings.Count(res.Text, "== archive=") != 1 {
		t.Fatalf("unmatched part or duplicate result: %q", res.Text)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("read created files or directories: entries=%v err=%v", entries, err)
	}
}

func TestReadZip_ReadReturnsEveryMatchWithArchiveIdentity(t *testing.T) {
	dir := t.TempDir()
	for name, text := range map[string]string{"a.zip": "first", "b.zip": "second"} {
		writeTestZip(t, dir, name, map[string]string{"nested/info.txt": text, "nested/": ""})
	}
	res, err := NewReadZip(dir, 0).Execute(context.Background(), json.RawMessage(`{"paths":["b.zip","a.zip"],"action":"read","pattern":"info.txt"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("result=%+v err=%v", res, err)
	}
	first, second := strings.Index(res.Text, `archive="b.zip"`), strings.Index(res.Text, `archive="a.zip"`)
	if first < 0 || second <= first || !strings.Contains(res.Text, "first\n") || !strings.Contains(res.Text, "second\n") {
		t.Fatalf("archive order or multiple matches lost: %q", res.Text)
	}
}

func TestReadZip_ReadRejectsInvalidRequests(t *testing.T) {
	dir := t.TempDir()
	writeTestZip(t, dir, "a.zip", map[string]string{"info.txt": "text"})
	tool := NewReadZip(dir, 0)
	for _, tc := range []struct {
		args string
		want string
	}{
		{`{"path":"a.zip","action":"read"}`, "requires a nonempty pattern"},
		{`{"path":"a.zip","action":"read","pattern":"  "}`, "requires a nonempty pattern"},
		{`{"path":"a.zip","action":"read","pattern":"["}`, "invalid read pattern"},
		{`{"path":"a.zip","paths":["a.zip"],"action":"read","pattern":"*.txt"}`, "not both"},
		{`{"paths":[],"action":"read","pattern":"*.txt"}`, "explicitly named archives"},
		{`{"paths":[""],"action":"read","pattern":"*.txt"}`, "must be nonempty"},
		{`{"paths":["a.zip","./a.zip"],"action":"read","pattern":"*.txt"}`, "duplicate archive"},
		{`{"path":"a.zip","action":"read","pattern":"*.txt","max_matches":17}`, "max_matches"},
		{`{"path":"a.zip","action":"read","pattern":"*.txt","max_text_bytes":65537}`, "max_text_bytes"},
		{`{"path":"a.zip","action":"read","pattern":"*.txt","max_entries":10001}`, "max_entries"},
		{`{"path":"../outside.zip","action":"read","pattern":"*.txt"}`, "archive"},
		{`{"paths":["a.zip"],"action":"extract"}`, "only supported for read"},
		{`{"paths":["a.zip"],"action":"list"}`, "only supported for read"},
	} {
		t.Run(tc.want+tc.args, func(t *testing.T) {
			res, err := tool.Execute(context.Background(), json.RawMessage(tc.args))
			if err == nil || res.Err == nil || !strings.Contains(err.Error(), tc.want) || res.Text != "" {
				t.Fatalf("args=%s result=%+v err=%v, want %q", tc.args, res, err, tc.want)
			}
		})
	}
	paths := make([]string, zipReadMaxArchives+1)
	for i := range paths {
		paths[i] = "a.zip"
	}
	args, _ := json.Marshal(map[string]any{"paths": paths, "action": "read", "pattern": "*.txt"})
	res, err := tool.Execute(context.Background(), args)
	if err == nil || res.Err == nil || !strings.Contains(err.Error(), "explicitly named archives") {
		t.Fatalf("too many archives accepted: result=%+v err=%v", res, err)
	}
}

func TestReadZip_ReadRejectsBinaryAndInvalidUTF8WithoutPartialOutput(t *testing.T) {
	for _, text := range []string{"prefix\x00binary", "\xff\xfe", "prefix\x01control"} {
		t.Run("invalid_text", func(t *testing.T) {
			dir := t.TempDir()
			writeTestZip(t, dir, "a.zip", map[string]string{"a.txt": "valid first entry", "z.txt": text})
			res, err := NewReadZip(dir, 0).Execute(context.Background(), json.RawMessage(`{"path":"a.zip","action":"read","pattern":"*.txt"}`))
			if err == nil || res.Err == nil || !strings.Contains(err.Error(), "not UTF-8 text") || res.Text != "" {
				t.Fatalf("invalid content or partial output exposed: result=%+v err=%v", res, err)
			}
		})
	}
}

func TestReadZip_ReadTextBudgetBoundaryAndCancellation(t *testing.T) {
	dir := t.TempDir()
	writeTestZip(t, dir, "a.zip", map[string]string{"text.txt": "żółw\t\r\n"})
	tool := NewReadZip(dir, 0)
	textBytes := len("żółw\t\r\n")
	args, _ := json.Marshal(map[string]any{"path": "a.zip", "action": "read", "pattern": "text.txt", "max_text_bytes": textBytes})
	res, err := tool.Execute(context.Background(), args)
	if err != nil || res.Err != nil || !strings.Contains(res.Text, "żółw\t\r\n") {
		t.Fatalf("exact byte budget changed UTF-8 text: result=%+v err=%v", res, err)
	}
	args, _ = json.Marshal(map[string]any{"path": "a.zip", "action": "read", "pattern": "text.txt", "max_text_bytes": textBytes - 1})
	res, err = tool.Execute(context.Background(), args)
	if err == nil || res.Err == nil || !strings.Contains(err.Error(), "matching text exceeds") {
		t.Fatalf("undersized budget accepted: result=%+v err=%v", res, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err = tool.Execute(ctx, json.RawMessage(`{"path":"a.zip","action":"read","pattern":"text.txt"}`))
	if err == nil || res.Err == nil || err != context.Canceled || res.Text != "" {
		t.Fatalf("canceled read accepted: result=%+v err=%v", res, err)
	}
}

func TestReadZip_ReadPreflightsLimitsBeforeOpeningAnyEntry(t *testing.T) {
	dir := t.TempDir()
	corrupt := writeTestZipRaw(t, dir, "a.zip", []struct {
		Name    string
		Content string
		Method  uint16
	}{{Name: "a.txt", Content: "proof", Method: zip.Store}})
	data, err := os.ReadFile(corrupt)
	if err != nil {
		t.Fatal(err)
	}
	at := bytes.Index(data, []byte("proof"))
	if at < 0 {
		t.Fatal("stored fixture payload missing")
	}
	data[at] = 'X' // Opening this entry fails CRC; preflight must happen first.
	if err := os.WriteFile(corrupt, data, 0o644); err != nil {
		t.Fatal(err)
	}
	other := writeTestZip(t, dir, "b.zip", map[string]string{"b.txt": "another"})
	for _, tc := range []struct {
		name string
		args string
		set  func(*ReadZipTool)
		want string
	}{
		{"matches", `{"paths":["a.zip","b.zip"],"action":"read","pattern":"*.txt","max_matches":1}`, nil, "matched files"},
		{"text", `{"paths":["a.zip","b.zip"],"action":"read","pattern":"*.txt","max_text_bytes":10}`, nil, "matching text exceeds"},
		{"entries", `{"paths":["a.zip","b.zip"],"action":"read","pattern":"*.txt","max_entries":1}`, nil, "exceed 1 entries"},
		{"archive_bytes", `{"paths":["a.zip","b.zip"],"action":"read","pattern":"*.txt"}`, func(tool *ReadZipTool) {
			a, _ := os.Stat(corrupt)
			b, _ := os.Stat(other)
			tool.MaxZipBytes = a.Size() + b.Size() - 1
		}, "bytes on disk"},
		{"per_file", `{"path":"a.zip","action":"read","pattern":"*.txt"}`, func(tool *ReadZipTool) { tool.MaxSingleFileBytes = 4 }, "per-file cap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := NewReadZip(dir, 0)
			if tc.set != nil {
				tc.set(tool)
			}
			res, err := tool.Execute(context.Background(), json.RawMessage(tc.args))
			if err == nil || res.Err == nil || !strings.Contains(err.Error(), tc.want) || res.Text != "" {
				t.Fatalf("preflight did not precede corrupt payload read: result=%+v err=%v want=%q", res, err, tc.want)
			}
		})
	}
	res, err := NewReadZip(dir, 0).Execute(context.Background(), json.RawMessage(`{"path":"a.zip","action":"read","pattern":"*.txt"}`))
	if err == nil || res.Err == nil || !strings.Contains(err.Error(), "checksum") || res.Text != "" {
		t.Fatalf("corrupted entry passed: result=%+v err=%v", res, err)
	}
}

func TestReadZip_ReadPreflightsOutputIncludingEntryNames(t *testing.T) {
	dir := t.TempDir()
	// ZIP permits long names; quoting control characters must count toward
	// the output cap even when the entry's text is empty.
	writeTestZip(t, dir, "a.zip", map[string]string{strings.Repeat("\t", 50000) + ".txt": ""})
	res, err := NewReadZip(dir, 0).Execute(context.Background(), json.RawMessage(`{"path":"a.zip","action":"read","pattern":"*.txt"}`))
	if err == nil || res.Err == nil || !strings.Contains(err.Error(), "output bytes") || res.Text != "" {
		t.Fatalf("entry names escaped output budget: result=%+v err=%v", res, err)
	}
}

func TestReadZip_ExtractNoMatchesIsInert(t *testing.T) {
	dir := t.TempDir()
	writeTestZip(t, dir, "a.zip", map[string]string{"info.txt": "text"})
	tool := NewReadZip(dir, 0)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.zip","action":"extract","pattern":"*.json","target_dir":"out"}`))
	if err != nil || res.Err != nil || !res.Inert || !strings.Contains(res.Text, "Extracted 0 entries") {
		t.Fatalf("empty extraction counted as progress: result=%+v err=%v", res, err)
	}
	res, err = tool.Execute(context.Background(), json.RawMessage(`{"path":"a.zip","action":"extract","pattern":"*.txt","target_dir":"out"}`))
	if err != nil || res.Err != nil || res.Inert {
		t.Fatalf("real extraction counted as inert: result=%+v err=%v", res, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "out", "info.txt"))
	if err != nil || string(data) != "text" {
		t.Fatalf("existing extraction contract changed: text=%q err=%v", data, err)
	}
}
