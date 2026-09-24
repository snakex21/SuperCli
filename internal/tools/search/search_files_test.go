package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools/core"
	"supercli/internal/tools/sandbox"
)

func TestFileDiscoveryThroughRegistry(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"src/a.go": "package a\n", "src/empty.go": "", "src/deep/b.go": "\x00binary",
		"src/.hidden/c.go": strings.Repeat("x", 2*1024*1024),
		"src/note.md":      "note", "docs/other.go": "package docs",
		"src/node_modules/skip.go": "", "src/.tmp/skip.go": "",
	} {
		writeSearchFixture(t, root, name, body)
	}
	reg := core.NewRegistry()
	reg.MustRegister(NewSearchCode(root).Spec())
	for _, args := range []string{
		`{"include":"src/**/*.go"}`,
		`{"query":"","path":"src","include":"*.go"}`,
	} {
		res, err := reg.Execute(context.Background(), "search_code", json.RawMessage(args))
		want := "src/.hidden/c.go\nsrc/a.go\nsrc/deep/b.go\nsrc/empty.go"
		if err != nil || res.Err != nil || res.Text != want {
			t.Fatalf("args=%s result=%+v err=%v", args, res, err)
		}
	}
	// File-root and explicitly selected excluded roots remain addressable.
	for _, tc := range []struct{ args, want string }{
		{`{"path":"src/empty.go","include":"*.go"}`, "src/empty.go"},
		{`{"path":"src/.tmp","include":"*.go"}`, "src/.tmp/skip.go"},
		{`{"include":"*.zig"}`, "no matches"},
	} {
		res, err := reg.Execute(context.Background(), "search_code", json.RawMessage(tc.args))
		if err != nil || res.Err != nil || res.Text != tc.want {
			t.Fatalf("%s: %+v %v", tc.args, res, err)
		}
	}
}

func TestFileDiscoveryLimitsAndValidation(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 55; i++ {
		writeSearchFixture(t, root, fmt.Sprintf("f%02d.go", i), "")
	}
	tool := NewSearchCode(root).Spec()
	for _, tc := range []struct {
		args string
		max  int
	}{
		{`{"include":"*.go"}`, 50},
		{`{"include":"*.go","max":2}`, 2},
		{`{"include":"*.go","max":55}`, 55},
	} {
		res, err := tool.Fn(context.Background(), json.RawMessage(tc.args))
		lines := strings.Split(res.Text, "\n")
		if err != nil || res.Err != nil || len(lines) != tc.max+1 ||
			!strings.Contains(lines[len(lines)-1], "results may be incomplete") {
			t.Fatalf("%s: %+v %v", tc.args, res, err)
		}
	}
	for _, args := range []string{
		`{}`, `{"query":""}`, `{"include":"../*.go"}`,
		`{"include":"*.go","context":1}`, `{"include":"*.go","path":"missing"}`,
	} {
		res, _ := tool.Fn(context.Background(), json.RawMessage(args))
		if res.Err == nil {
			t.Fatalf("%s silently succeeded: %+v", args, res)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, _ := tool.Fn(ctx, json.RawMessage(`{"include":"*.go"}`))
	if !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("cancellation: %+v", res)
	}
}

func TestFileDiscoverySandbox(t *testing.T) {
	root := t.TempDir()
	writeSearchFixture(t, root, "workspace/inside.go", "")
	writeSearchFixture(t, root, "outside/secret.go", "")
	tool := NewSearchCode(filepath.Join(root, "workspace")).Spec()
	args, _ := json.Marshal(map[string]string{"include": "*.go", "path": filepath.Join(root, "outside")})
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(false) })
	res, _ := tool.Fn(context.Background(), args)
	if res.Err == nil {
		t.Fatal("external root allowed while sandboxed")
	}
	sandbox.SetUnsandboxed(true)
	res, _ = tool.Fn(context.Background(), args)
	if res.Err != nil || res.Text != filepath.Join(root, "outside", "secret.go") {
		t.Fatalf("external allowed path must remain absolute: %+v", res)
	}
}

func TestFileDiscoverySkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	writeSearchFixture(t, root, "src/real.go", "")
	writeSearchFixture(t, root, "outside/secret.go", "")
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(root, "src", "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "outside", "secret.go"), filepath.Join(root, "src", "link.go")); err != nil {
		t.Fatal(err)
	}
	res, _ := NewSearchCode(root).Spec().Fn(context.Background(), json.RawMessage(`{"path":"src","include":"*.go"}`))
	if res.Err != nil || res.Text != "src/real.go" {
		t.Fatalf("followed symlink: %+v", res)
	}
}

// Compare finding the same 256 paths with the observed ^package search and with
// directory metadata. Both complete the same fixture; no empty files here.
func BenchmarkFileDiscovery(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 256; i++ {
		writeSearchFixture(b, root, fmt.Sprintf("src/group%02d/file%03d.go", i/16, i),
			strings.Repeat("// license and documentation\n", 256)+"package example\n"+strings.Repeat("// code\n", 256))
	}
	s := NewSearchCode(root)
	g, err := compileSearchGlob("src/**/*.go")
	if err != nil {
		b.Fatal(err)
	}
	for _, mode := range []string{"content", "paths"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var res Result
				var err error
				if mode == "content" {
					res, err = s.fallback(context.Background(), root, "^package ", 500, &searchContext{include: g})
				} else {
					res, err = s.findFiles(context.Background(), root, g, 500)
				}
				if err != nil || res.Err != nil || len(strings.Split(res.Text, "\n")) != 256 {
					b.Fatalf("result=%+v err=%v", res, err)
				}
				b.ReportMetric(float64(len(res.Text)), "output-B")
			}
		})
	}
}
