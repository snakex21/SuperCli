package preflight

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func noGit(string) (string, error)  { return "", fmt.Errorf("git: not found") }
func hasGit(string) (string, error) { return `C:\git\git.exe`, nil }
func writePF(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitStub returns canned outputs per subcommand.
func gitStub(branch, head, status, log string) func(string, ...string) (string, error) {
	return func(_ string, args ...string) (string, error) {
		switch args[0] {
		case "rev-parse":
			if branch == "" {
				return "", fmt.Errorf("not a repo")
			}
			return branch, nil
		case "status":
			return status, nil
		case "log":
			for _, a := range args {
				if a == "-1" {
					return head, nil
				}
			}
			return log, nil
		}
		return "", fmt.Errorf("unexpected git %v", args)
	}
}

// Git mode: the block carries branch, HEAD, changes and commits.
func TestBuild_GitMode(t *testing.T) {
	block := Build(t.TempDir(), Options{
		LookPath: hasGit,
		RunGit: gitStub("main", "abc1234 fix the bug",
			" M internal/app/main.go", "abc1234 fix the bug\nddd5678 older commit"),
	})
	for _, want := range []string{"branch: main", "HEAD: abc1234", "M internal/app/main.go", "recent commits:", "ddd5678"} {
		if !strings.Contains(block, want) {
			t.Errorf("block missing %q:\n%s", want, block)
		}
	}
}

// The token budget is a HARD cap: a repo with huge git output must
// still produce a block within budget, trimmed from the tail.
func TestBuild_BudgetEnforced(t *testing.T) {
	var big strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&big, "commit%04d this is a reasonably long oneline subject with words\n", i)
	}
	block := Build(t.TempDir(), Options{
		Budget:   150,
		LookPath: hasGit,
		RunGit:   gitStub("main", "abc1234 head", "", big.String()),
	})
	if block == "" {
		t.Fatal("block empty")
	}
	if got := EstimateTokens(block); got > 150 {
		t.Errorf("budget exceeded: %d tok > 150", got)
	}
	// The most important part survived the trim.
	if !strings.Contains(block, "branch: main") {
		t.Errorf("branch line lost in the trim:\n%s", block)
	}
}

// No git binary = pure-Go fallback: recently modified files, newest
// first. Simulates a machine without git via LookPath.
func TestBuild_FallbackWithoutGit(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.go")
	fresh := filepath.Join(dir, "fresh.go")
	writePF(t, old, "package a")
	writePF(t, fresh, "package a")
	past := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	block := Build(dir, Options{LookPath: noGit})
	if !strings.Contains(block, "recently modified files:") {
		t.Fatalf("fallback header missing:\n%s", block)
	}
	fi := strings.Index(block, "fresh.go")
	oi := strings.Index(block, "old.go")
	if fi < 0 || oi < 0 {
		t.Fatalf("files missing:\n%s", block)
	}
	if fi > oi {
		t.Errorf("files not newest-first:\n%s", block)
	}
}

// git present but the directory is not a repo = same fallback.
func TestBuild_NotARepoFallsBack(t *testing.T) {
	dir := t.TempDir()
	writePF(t, filepath.Join(dir, "a.txt"), "x")
	block := Build(dir, Options{LookPath: hasGit, RunGit: gitStub("", "", "", "")})
	if !strings.Contains(block, "recently modified files:") {
		t.Errorf("non-repo dir must use the mtime fallback:\n%s", block)
	}
}

// An empty dir with no git yields no block at all (caller skips).
func TestBuild_EmptyTree(t *testing.T) {
	if block := Build(t.TempDir(), Options{LookPath: noGit}); block != "" {
		t.Errorf("empty tree should yield an empty block, got:\n%s", block)
	}
}

// A clean repo says so instead of listing nothing.
func TestBuild_CleanTree(t *testing.T) {
	block := Build(t.TempDir(), Options{
		LookPath: hasGit,
		RunGit:   gitStub("main", "abc1234 head", "", "abc1234 head"),
	})
	if !strings.Contains(block, "working tree clean") {
		t.Errorf("clean repo should state it:\n%s", block)
	}
}
