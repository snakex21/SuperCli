package search

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// This test binary acts as rg only in the explicitly marked child process.
// init handles rg flags before the testing flag parser sees them.
func init() {
	mode := os.Getenv("SUPERCLI_SEARCH_RG_HELPER")
	if mode == "" {
		return
	}
	args := strings.Join(os.Args[1:], " ")
	for _, flag := range []string{"--with-filename", "--color=never", "!.zig-cache/**", "!zig-out/**"} {
		if !strings.Contains(args, flag) {
			fmt.Fprintln(os.Stderr, "missing expected flag", flag)
			os.Exit(2)
		}
	}
	root := os.Args[len(os.Args)-1]
	switch mode {
	case "focus":
		cwd, _ := os.Getwd()
		if !strings.Contains(args, "--glob *.go") || cwd != root {
			os.Exit(2)
		}
		fmt.Printf("%s/a.md\x001:needle excluded\n%s/b.go\x001:needle first\n%s/b.go\x002:needle second\n", root, root, root)
	case "fail":
		os.Exit(2)
	case "context":
		if !strings.Contains(args, "--null") {
			os.Exit(2)
		}
		fmt.Printf("%s\x002:needle\n", root)
	case "single":
		fmt.Printf("%s:2:needle\n", root)
	case "long":
		// Exceeds Scanner's token limit, then keeps writing. Wait before
		// cancel/drain would hang on the unread stdout pipe.
		for i := 0; i < 512; i++ {
			if _, err := fmt.Fprint(os.Stdout, strings.Repeat("x", 32768)); err != nil {
				os.Exit(0)
			}
		}
	case "cancel":
		time.Sleep(10 * time.Second)
	}
	os.Exit(0)
}

func TestSearchRGSingleFileIncludesReadablePath(t *testing.T) {
	dir := t.TempDir()
	path := writeSearchFixture(t, dir, "a.zig", "first\nneedle\n")
	t.Setenv("SUPERCLI_SEARCH_RG_HELPER", "single")
	result, err := NewSearchCode(dir).ripgrep(context.Background(), os.Args[0], path, "needle", 10)
	if err != nil || result.Err != nil || result.Text != "a.zig:2:needle" {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestSearchRGScannerFailureStopsChildAndUsesFallback(t *testing.T) {
	dir := t.TempDir()
	writeSearchFixture(t, dir, "a.zig", "needle\n")
	t.Setenv("SUPERCLI_SEARCH_RG_HELPER", "long")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := NewSearchCode(dir).ripgrep(ctx, os.Args[0], dir, "needle", 10)
	if err != nil || result.Err != nil || result.Text != "a.zig:1:needle" {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestSearchRGCancelledDoesNotReportNoMatches(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SUPERCLI_SEARCH_RG_HELPER", "cancel")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	result, err := NewSearchCode(dir).ripgrep(ctx, os.Args[0], dir, "needle", 10)
	if err != nil || result.Err == nil || result.Text == "no matches" {
		t.Fatalf("%+v %v", result, err)
	}
}
