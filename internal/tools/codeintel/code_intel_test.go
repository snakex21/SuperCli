package codeintel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"supercli/internal/tools/core"
)

func TestSpecIsSingleLazyTool(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	spec := tool.Spec()
	if spec.Name != "code_intel" || spec.ReadOnly {
		t.Fatalf("spec = (%q, readOnly=%v)", spec.Name, spec.ReadOnly)
	}
	if !strings.Contains(spec.Schema, "diagnostics") || !strings.Contains(spec.Schema, "outline") || !strings.Contains(spec.Schema, "symbols") {
		t.Fatalf("schema missing actions: %s", spec.Schema)
	}
}

func TestWrapMutationAppendsOnlyWarmDiagnostics(t *testing.T) {
	var checked string
	tool := &Tool{BaseDir: t.TempDir(), warmCheck: func(_ context.Context, path string) string {
		checked = path
		return "main.go:3:2 error: undefined: thing"
	}}
	spec := tool.WrapMutation(core.Tool{
		Name: "write_file", Description: "test", Schema: `{}`,
		Fn: func(context.Context, json.RawMessage) (core.Result, error) {
			return core.Result{Text: "wrote main.go"}, nil
		},
	})
	res, err := spec.Fn(context.Background(), json.RawMessage(`{"path":"main.go","content":"x"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("execute: result=%+v err=%v", res, err)
	}
	if checked != "main.go" || !strings.Contains(res.Text, "[LSP diagnostics after edit]") || !strings.Contains(res.Text, "undefined: thing") {
		t.Fatalf("checked=%q result=%q", checked, res.Text)
	}
}

func TestWrapMutationPreservesSuccessfulOutputWhenLSPIsCold(t *testing.T) {
	tool := &Tool{warmCheck: func(context.Context, string) string { return "" }}
	spec := tool.WrapMutation(core.Tool{
		Name: "edit_line", Description: "test", Schema: `{}`,
		Fn: func(context.Context, json.RawMessage) (core.Result, error) {
			return core.Result{Text: "unchanged-contract"}, nil
		},
	})
	res, err := spec.Fn(context.Background(), json.RawMessage(`{"path":"main.go"}`))
	if err != nil || res.Text != "unchanged-contract" {
		t.Fatalf("result=%+v err=%v", res, err)
	}
}

func TestServersDoesNotNeedPathOrStartProcess(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"servers"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("execute: err=%v result=%v", err, res.Err)
	}
	if !strings.Contains(res.Text, "gopls:") {
		t.Fatalf("status = %q", res.Text)
	}
}

func TestUnsupportedExtensionDegradesCleanly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := New(dir)
	defer tool.Close()
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"diagnostics","path":"x.txt"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("execute: err=%v result=%v", err, res.Err)
	}
	if !strings.Contains(res.Text, "unsupported") {
		t.Fatalf("result = %q", res.Text)
	}
}

func TestPathCannotEscapeWorkspace(t *testing.T) {
	tool := New(t.TempDir())
	defer tool.Close()
	path := "../outside.go"
	if runtime.GOOS == "windows" {
		path = `..\outside.go`
	}
	raw, _ := json.Marshal(map[string]any{"action": "diagnostics", "path": path})
	res, _ := tool.Execute(context.Background(), raw)
	if res.Err == nil {
		t.Fatal("expected sandbox error")
	}
}

func TestDisplayPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pkg", "x.go")
	if got := displayPath(dir, path); got != filepath.Join("pkg", "x.go") {
		t.Fatalf("displayPath = %q", got)
	}
}
