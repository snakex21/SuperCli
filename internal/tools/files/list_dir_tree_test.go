package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func treeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{
		"README.md", "src/auth/login.go", "src/db/store.go",
		"src/auth/deep/hidden.go", "node_modules/pkg/ignored.js",
		".zig-cache/hash/ignored.zig", "build/generated.go",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, path, "test")
	}
	return root
}

func TestListDirTreeDepthAndDefault(t *testing.T) {
	root := treeFixture(t)
	tool := NewListDir(root)
	defaultOut := runListDir(t, tool, "{}").Text
	if explicit := runListDir(t, tool, `{"depth":1}`).Text; explicit != defaultOut {
		t.Fatalf("depth 1 changed default listing:\n%s\n%s", defaultOut, explicit)
	}
	if strings.Contains(defaultOut, "src/auth") || strings.Contains(defaultOut, "not expanded") {
		t.Fatal("default listing expanded or changed folder labels")
	}
	depth2 := runListDir(t, tool, `{"depth":2}`).Text
	if !strings.Contains(depth2, "src/auth/ (depth limit)") || strings.Contains(depth2, "login.go") || strings.Contains(depth2, "complete tree") {
		t.Fatalf("wrong depth 2: %s", depth2)
	}
	depth3 := runListDir(t, tool, `{"depth":3}`).Text
	for _, expected := range []string{"src/auth/login.go (4 bytes)", "src/db/store.go", "src/auth/deep/", "node_modules/ (not expanded)", ".zig-cache/ (not expanded)", "build/ (not expanded)"} {
		if !strings.Contains(depth3, expected) {
			t.Errorf("missing %q: %s", expected, depth3)
		}
	}
	for _, absent := range []string{"hidden.go", "ignored.js", "ignored.zig", "generated.go"} {
		if strings.Contains(depth3, absent) {
			t.Errorf("unexpected %q: %s", absent, depth3)
		}
	}
	if out := runListDir(t, tool, `{"depth":4}`).Text; !strings.Contains(out, "src/auth/deep/hidden.go") || !strings.Contains(out, "complete tree") {
		t.Fatalf("depth 4 did not include deepest file: %s", out)
	}
}

func TestListDirTreeExplicitIgnoredRoot(t *testing.T) {
	out := runListDir(t, NewListDir(treeFixture(t)), `{"path":"node_modules","depth":3}`).Text
	if !strings.Contains(out, "pkg/ignored.js") {
		t.Fatalf("explicit dependency root should remain accessible: %s", out)
	}
}

func TestListDirTreeSharedLimitAndBreadth(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a/one.go", "a/two.go", "b/three.go", "z.go"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, path, "")
	}
	tool := NewListDir(root)
	tool.MaxEntries = 4
	out := runListDir(t, tool, `{"depth":4}`).Text
	for _, expected := range []string{"a/", "a/one.go", "b/", "z.go", "contains 4 item(s)", "listing incomplete"} {
		if !strings.Contains(out, expected) {
			t.Errorf("missing %q: %s", expected, out)
		}
	}
	if strings.Contains(out, "two.go") || strings.Contains(out, "three.go") {
		t.Fatalf("global limit exceeded: %s", out)
	}
	tool.MaxEntries = 6
	if out := runListDir(t, tool, `{"depth":4}`).Text; strings.Contains(out, "incomplete") {
		t.Fatalf("exact complete listing mislabeled truncated: %s", out)
	}
}

func TestListDirTreeRelativeBase(t *testing.T) {
	root := treeFixture(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(cwd, root)
	if err != nil {
		t.Fatal(err)
	}
	out := runListDir(t, NewListDir(rel), `{"depth":3}`).Text
	if !strings.Contains(out, "src/auth/login.go") {
		t.Fatal(out)
	}
}

func TestListDirTreeLimitsAndCancellation(t *testing.T) {
	tool := NewListDir(treeFixture(t))
	for _, raw := range []string{`{"depth":0}`, `{"depth":-1}`, `{"depth":5}`, `{"depth":1.5}`} {
		if res, err := tool.Execute(context.Background(), json.RawMessage(raw)); err == nil || res.Err == nil {
			t.Errorf("invalid depth accepted: %s", raw)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if res, err := tool.Execute(ctx, json.RawMessage(`{"depth":4}`)); err != context.Canceled || res.Err != err {
		t.Fatalf("cancellation not propagated: %+v, %v", res, err)
	}
	if out := runListDir(t, tool, `{"path":"README.md","depth":4}`).Text; !strings.Contains(out, "is a file") {
		t.Fatal(out)
	}
	if out := runListDir(t, NewListDir(t.TempDir()), `{"depth":4}`).Text; !strings.Contains(out, "is empty") {
		t.Fatal(out)
	}
}

func TestListDirTreeDoesNotFollowLinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(outside, "outside-secret.txt"), "secret")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	out := runListDir(t, NewListDir(root), `{"depth":4}`).Text
	if !strings.Contains(out, "link (symlink; not expanded)") || strings.Contains(out, "outside-secret") {
		t.Fatalf("symlink followed or unmarked: %s", out)
	}
}
