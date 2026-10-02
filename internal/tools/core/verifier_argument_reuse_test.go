package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifierArgumentReusePreservesAliasesAndJSONSemantics(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"current.txt": "ABC current content", "fallback.txt": "XYZ fallback content", "empty.txt": ""} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, tool, args string
		emptyExpected    bool
		ok               bool
		reason           string
	}{
		{name: "path priority", args: `{"path":"current.txt","file":"missing.txt"}`, ok: true},
		{name: "missing primary path", args: `{"path":"missing.txt","file":"current.txt"}`, reason: "file does not exist"},
		{name: "empty primary fallback", args: `{"path":"","file":"current.txt"}`, ok: true},
		{name: "null primary fallback", args: `{"path":null,"file":"current.txt"}`, ok: true},
		{name: "wrong primary type fallback", args: `{"path":{},"file":"current.txt"}`, ok: true},
		{name: "array primary fallback", args: `{"path":["missing.txt"],"file":"current.txt"}`, ok: true},
		{name: "destination before dest", args: `{"destination":"current.txt","dest":"missing.txt"}`, ok: true},
		{name: "dest before filepath", args: `{"dest":"current.txt","filepath":"missing.txt"}`, ok: true},
		{name: "filepath alias", args: `{"filepath":"current.txt"}`, ok: true},
		{name: "upper path ignored", args: `{"PATH":"missing.txt","file":"current.txt"}`, ok: true},
		{name: "mixed path ignored", args: `{"Path":"missing.txt","path":"current.txt"}`, ok: true},
		{name: "differently cased duplicate ignored", args: `{"path":"current.txt","PATH":"missing.txt"}`, ok: true},
		{name: "escaped exact key", args: `{"p\u0061th":"current.txt"}`, ok: true},
		{name: "duplicate path last wins", args: `{"path":"missing.txt","path":"current.txt"}`, ok: true},
		{name: "duplicate path null wins", args: `{"path":"missing.txt","path":null,"file":"current.txt"}`, ok: true},
		{name: "duplicate path wrong type wins", args: `{"path":"missing.txt","path":42,"file":"current.txt"}`, ok: true},
		{name: "expected priority", args: `{"path":"current.txt","expected_content":"ABC","must_contain":"absent"}`, ok: true},
		{name: "empty expected stops aliases", args: `{"path":"current.txt","expected_content":"","must_contain":"absent"}`, ok: true},
		{name: "null expected fallback", args: `{"path":"current.txt","expected_content":null,"must_contain":"ABC"}`, ok: true},
		{name: "wrong expected type fallback", args: `{"path":"current.txt","expected_content":false,"must_contain":"ABC"}`, ok: true},
		{name: "contains alias", args: `{"path":"current.txt","contains":"ABC"}`, ok: true},
		{name: "expected alias", args: `{"path":"current.txt","expected":"absent"}`, reason: "does not contain"},
		{name: "upper expected ignored", args: `{"path":"current.txt","EXPECTED_CONTENT":"absent"}`, ok: true},
		{name: "duplicate expected last wins", args: `{"path":"current.txt","expected_content":"absent","expected_content":"ABC"}`, ok: true},
		{name: "duplicate expected null falls back", args: `{"path":"current.txt","expected_content":"absent","expected_content":null,"contains":"ABC"}`, ok: true},
		{name: "malformed object remains unavailable", args: `{"path":"missing.txt",`, ok: true},
		{name: "trailing invalid JSON", args: `{"path":"missing.txt"} trailing`, ok: true},
		{name: "null object", args: "null", ok: true},
		{name: "array object", args: `["missing.txt"]`, ok: true},
		{name: "number object", args: "42", ok: true},
		{name: "empty args", ok: true},
		{name: "write empty content", tool: "write_file", args: `{"path":"empty.txt","content":""}`, ok: true},
		{name: "null content does not authorize empty", tool: "write_file", args: `{"path":"empty.txt","content":null}`, reason: "is empty"},
		{name: "wrong content type does not authorize empty", tool: "write_file", args: `{"path":"empty.txt","content":0}`, reason: "is empty"},
		{name: "upper content ignored", tool: "write_file", args: `{"path":"empty.txt","CONTENT":""}`, reason: "is empty"},
		{name: "duplicate content last wins", tool: "write_file", args: `{"path":"empty.txt","content":"body","content":""}`, ok: true},
		{name: "create empty legacy rule unchanged", tool: "create_file", args: `{"path":"empty.txt","content":""}`, reason: "is empty"},
		{name: "explicit empty expectation", tool: "create_file", args: `{"path":"empty.txt"}`, emptyExpected: true, ok: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := tc.tool
			if tool == "" {
				tool = "patch_file"
			}
			verdict := (DefaultVerifier{}).Verify(Check{Family: "file_write", Tool: tool, Args: json.RawMessage(tc.args), BaseDir: dir, Result: Result{Text: "written", EmptyFileExpected: tc.emptyExpected}})
			if verdict.OK != tc.ok || (tc.reason != "" && !strings.Contains(verdict.Reason, tc.reason)) || (tc.ok && verdict.Reason != "") {
				t.Fatalf("verdict=%+v, want ok=%v reason containing %q", verdict, tc.ok, tc.reason)
			}
		})
	}
}

func BenchmarkVerifierArgumentReuse(b *testing.B) {
	for _, kind := range []string{"small-content", "1MiB-content", "20-change-patch"} {
		b.Run(kind, func(b *testing.B) {
			dir := b.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "current.txt"), []byte("current content"), 0600); err != nil {
				b.Fatal(err)
			}
			args := map[string]any{"path": "current.txt"}
			tool := "write_file"
			switch kind {
			case "small-content":
				args["content"] = strings.Repeat("x", 128)
			case "1MiB-content":
				args["content"] = strings.Repeat("x", 1024*1024)
			case "20-change-patch":
				tool = "patch_file"
				var changes []map[string]string
				for i := 0; i < 20; i++ {
					changes = append(changes, map[string]string{"old": strings.Repeat("a", 26214), "new": strings.Repeat("b", 26214)})
				}
				args["changes"] = changes
			}
			raw, err := json.Marshal(args)
			if err != nil {
				b.Fatal(err)
			}
			check := Check{Family: "file_write", Tool: tool, Args: raw, BaseDir: dir, Result: Result{Text: "written"}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := (DefaultVerifier{}).Verify(check); !got.OK {
					b.Fatal(got)
				}
			}
		})
	}
}
