package preflight

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStartupGitReadsOverlapAndStatusIsFresh(t *testing.T) {
	statusStarted := make(chan struct{})
	block := Build(t.TempDir(), Options{LookPath: hasGit, RunGit: func(_ string, args ...string) (string, error) {
		switch args[0] {
		case "status":
			close(statusStarted)
			return " M fresh.go", nil
		case "rev-parse":
			select {
			case <-statusStarted:
			case <-time.After(time.Second):
				t.Error("status read waited for identity reads")
			}
			return "main", nil
		case "log":
			return "abc123 newest", nil
		}
		return "", fmt.Errorf("unexpected command %v", args)
	}})
	for _, want := range []string{"branch: main", "HEAD: abc123 newest", "M fresh.go"} {
		if !strings.Contains(block, want) {
			t.Fatalf("missing %q: %s", want, block)
		}
	}
}

func TestStartupIncompleteGitStatusIsNotClean(t *testing.T) {
	block := Build(t.TempDir(), Options{LookPath: hasGit, RunGit: func(_ string, args ...string) (string, error) {
		switch args[0] {
		case "rev-parse":
			return "main", nil
		case "log":
			return "abc123 newest", nil
		}
		return "", context.DeadlineExceeded
	}})
	if !strings.Contains(block, "branch: main") || strings.Contains(block, "working tree clean") {
		t.Fatalf("misleading status: %s", block)
	}
}

func TestStartupCanceledBeforeCollection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if block := BuildContext(ctx, t.TempDir(), Options{LookPath: func(string) (string, error) { t.Fatal("started after cancellation"); return "", nil }}); block != "" {
		t.Fatal(block)
	}
	if _, err := realRunGit(ctx, t.TempDir(), "status", "--porcelain"); !errors.Is(err, context.Canceled) {
		t.Fatalf("subprocess ignored cancellation: %v", err)
	}
}

func TestStartupRecentFilesKeepsNewestEntries(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	for i := 0; i < 30; i++ {
		p := filepath.Join(root, fmt.Sprintf("file-%02d.txt", i))
		writePF(t, p, "x")
		ts := now.Add(time.Duration(i-30) * time.Minute)
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	got, complete := recentFiles(context.Background(), root, 10, now)
	if !complete || len(got) != 10 || !strings.HasPrefix(got[0], "file-29.txt") || !strings.HasPrefix(got[9], "file-20.txt") {
		t.Fatalf("complete=%v got=%v", complete, got)
	}
}

func TestStartupLargeFallbackIsLabeledPartial(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < defaultMaxScanEntries+1; i++ {
		writePF(t, filepath.Join(root, fmt.Sprintf("file-%04d.txt", i)), "x")
	}
	block := Build(root, Options{LookPath: noGit})
	if !strings.Contains(block, "recent files (partial scan):") || strings.Contains(block, "recently modified files:") {
		t.Fatalf("partial scan presented as exhaustive: %s", block)
	}
	if EstimateTokens(block) > DefaultBudget {
		t.Fatalf("budget exceeded: %s", block)
	}
}
