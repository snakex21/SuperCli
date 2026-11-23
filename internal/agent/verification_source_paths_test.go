package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/files"
)

// goalCloseFixture: a passing build check, then file edits through the real
// write tool, then the question "may the goal be closed?".
func goalCloseFixture(t *testing.T, dir string) (invoke func(name string, args any) toolResult, l *Loop) {
	t.Helper()
	reg := tools.NewRegistry()
	reg.MustRegister(files.NewWriteFile(dir).Spec())
	l, err := NewLoop(LoopConfig{Provider: echoProvider("test"), Registry: reg, BaseDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 64)
	go func() {
		for range events {
		}
	}()
	t.Cleanup(func() { close(events) })
	invoke = func(name string, args any) toolResult {
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		return l.invoke(context.Background(), llm.ToolCall{ID: name, Name: name, Arguments: string(raw)}, events)
	}
	check, pass := executionFixture(dir, []string{"zig", "build", "client", "--prefix", filepath.Join(dir, "zig-out")}, "", 0)
	l.recordCheckResult(check, pass)
	if l.failedChecks.unresolved() {
		t.Fatal("passing build left the goal blocked")
	}
	return invoke, l
}

// A build writes its own outputs (packages, logs, caches) through commands;
// those never make the checks stale, and neither does editing a generated
// file in an output folder with a file tool.
func TestGoalCloseIgnoresBuildOutputs(t *testing.T) {
	dir := t.TempDir()
	invoke, l := goalCloseFixture(t, dir)
	for _, path := range []string{"zig-out/bin/notes.txt", ".zig-cache/tmp/x.log", "node_modules/pkg/index.js"} {
		if r := invoke("write_file", map[string]any{"path": path, "content": "generated\n"}); r.failed {
			t.Fatalf("write %s: %+v", path, r)
		}
	}
	if l.failedChecks.unresolved() {
		t.Fatalf("generated files blocked closing the goal: %s", l.failedChecks.blocker())
	}
	if toolKind("ctx_execute") == "mutation" || toolKind("process_session") == "mutation" {
		t.Fatal("a command (build, package, test) must never count as a source edit")
	}
}

// In a git work tree, files the project ignores are not source either.
func TestGoalCloseIgnoresGitIgnoredFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("build-migration/\n*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	invoke, l := goalCloseFixture(t, dir)
	for _, path := range []string{"build-migration/survival-package-20261123-1400/run.txt", "trace.log"} {
		if r := invoke("write_file", map[string]any{"path": path, "content": "x\n"}); r.failed {
			t.Fatalf("write %s: %+v", path, r)
		}
	}
	if l.failedChecks.unresolved() {
		t.Fatalf("git-ignored files blocked closing the goal: %s", l.failedChecks.blocker())
	}
	// A source file in the same repository still counts.
	if r := invoke("write_file", map[string]any{"path": "src/main.zig", "content": "pub fn main() void {}\n"}); r.failed {
		t.Fatalf("write source: %+v", r)
	}
	if !l.failedChecks.unresolved() {
		t.Fatal("a source edit after the check did not block closing the goal")
	}
}

// A real source edit after the last passing check blocks, and the message
// names the files and the exact check to run again.
func TestGoalCloseBlocksRealSourceEditWithFileList(t *testing.T) {
	dir := t.TempDir()
	invoke, l := goalCloseFixture(t, dir)
	for _, path := range []string{"zig-engine/src/native_survival_client_smoke.zig", "zig-engine/src/native_player_live.zig"} {
		if r := invoke("write_file", map[string]any{"path": path, "content": "// edited\n"}); r.failed {
			t.Fatalf("write %s: %+v", path, r)
		}
	}
	if !l.failedChecks.unresolved() {
		t.Fatal("source edits after the check did not block closing the goal")
	}
	msg := l.failedChecks.blocker()
	for _, want := range []string{
		"code changed since verification",
		"zig-engine/src/native_survival_client_smoke.zig, zig-engine/src/native_player_live.zig",
		"`zig build client --prefix",
		"Run that check again on the current code",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("blocker %q lacks %q", msg, want)
		}
	}
	// Rerunning that check on the current code clears it and the file list.
	check, pass := executionFixture(dir, []string{"zig", "build", "client", "--prefix", filepath.Join(dir, "zig-out")}, "", 0)
	l.recordCheckResult(check, pass)
	if l.failedChecks.unresolved() {
		t.Fatalf("a passing rerun did not clear the block: %s", l.failedChecks.blocker())
	}
	if len(l.failedChecks.changedFiles) != 0 {
		t.Fatalf("changed files kept after re-verification: %v", l.failedChecks.changedFiles)
	}
}
