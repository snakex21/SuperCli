package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"supercli/internal/tools/core"
	"supercli/internal/tools/sandbox"
)

func TestMatchingFilesFindsSourcePastREADMEFlood(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	writeSearchFixture(t, root, "README.md", strings.Repeat("ResolveWidget documentation reference\n", 80))
	writeSearchFixture(t, root, "src/widget.go", "func ResolveWidget() string { return \"target\" }\n")
	tool := NewSearchCode(root)
	lines, err := tool.run(context.Background(), json.RawMessage(`{"query":"ResolveWidget","max":20,"context":0}`))
	if err != nil || lines.Err != nil || !strings.Contains(lines.Text, "limit reached") ||
		strings.Contains(lines.Text, "src/widget.go") {
		t.Fatalf("line-search fixture changed: %+v %v", lines, err)
	}
	for _, contextArg := range []string{"", `,"context":20`, `,"context":-1`} {
		args := json.RawMessage(`{"query":"ResolveWidget","max":20,"output_mode":"files"` + contextArg + `}`)
		result, err := tool.run(context.Background(), args)
		want := "README.md\nsrc/widget.go"
		if err != nil || result.Err != nil || result.Text != want ||
			result.ModelText != "" || result.ModelPreview != "" || result.RetainedText != "" {
			t.Fatalf("matching-file result: %+v %v", result, err)
		}
		view := core.NewOutputStore().ModelContent("search_code", result)
		if view != want || core.StoredOutputHandle(view) != "" {
			t.Fatal("small matching-file result forced retention")
		}
		t.Logf("context=%q lines-model=%d files-model=%d bytes; source found in first files call",
			contextArg, len(core.NewOutputStore().ModelContent("search_code", lines)), len(view))
	}
}

func TestMatchingFilesStopsAtFirstHitInEachFile(t *testing.T) {
	root := t.TempDir()
	// A full line search must diagnose the overlong later line. Finding whether
	// this file contains the symbol already completed on line one.
	writeSearchFixture(t, root, "first.go", "needle\n"+strings.Repeat("x", 1024*1024+1)+"\n")
	writeSearchFixture(t, root, "second.go", strings.Repeat("needle repeated\n", 1000))
	tool := NewSearchCode(root)
	files, err := tool.fallback(context.Background(), root, "needle", 50, &searchContext{filesOnly: true})
	if err != nil || files.Err != nil || files.Text != "first.go\nsecond.go" {
		t.Fatalf("file search scanned past its answer: %+v %v", files, err)
	}
	lines, err := tool.fallback(context.Background(), root, "needle", 50)
	if err != nil || lines.Err == nil || !strings.Contains(lines.Err.Error(), "token too long") {
		t.Fatalf("line search errors changed: %+v %v", lines, err)
	}
}

func TestMatchingFilesRealRGParityAndPolicies(t *testing.T) {
	rg := os.Getenv("SUPERCLI_TEST_RG")
	if rg == "" {
		t.Skip("set SUPERCLI_TEST_RG")
	}
	root := t.TempDir()
	for name, content := range map[string]string{
		"README.md":                            "ResolveWidget\n",
		"src/a.go":                             strings.Repeat("ResolveWidget repeated\n", 50),
		"src/deep/b.go":                        "before\nResolveWidget\nafter\n",
		"src/no.go":                            "unrelated\n",
		"src/empty.go":                         "",
		"src/payload.go":                       "\x00ResolveWidget\n",
		"src/node_modules/skip.go":             "ResolveWidget\n",
		"src/.tmp/skip.go":                     "ResolveWidget\n",
		"src/.claude/worktrees/worker/skip.go": "ResolveWidget\n",
	} {
		writeSearchFixture(t, root, name, content)
	}
	for _, tc := range []struct{ args, want string }{
		{`{"query":"ResolveWidget","output_mode":"files","include":"src/**/*.go","max":20}`, "src/a.go\nsrc/deep/b.go"},
		{`{"query":"ResolveWidget","output_mode":"files","path":"src/a.go","max":20}`, "src/a.go"},
		{`{"query":"ResolveWidget","output_mode":"files","path":"src/.tmp","max":20}`, "src/.tmp/skip.go"},
		{`{"query":"ResolveWidget","output_mode":"files","path":"src/.claude/worktrees/worker","max":20}`, "src/.claude/worktrees/worker/skip.go"},
		{`{"query":"missing_target","output_mode":"files","max":20}`, "no matches"},
		{`{"query":"","include":"src/**/*.go","output_mode":"files","max":20}`, "src/a.go\nsrc/deep/b.go\nsrc/empty.go\nsrc/no.go\nsrc/payload.go"},
	} {
		t.Setenv("PATH", filepath.Dir(rg))
		native, err := NewSearchCode(root).run(context.Background(), json.RawMessage(tc.args))
		if err != nil || native.Err != nil {
			t.Fatalf("%s native: %+v %v", tc.args, native, err)
		}
		t.Setenv("PATH", t.TempDir())
		fallback, err := NewSearchCode(root).run(context.Background(), json.RawMessage(tc.args))
		if err != nil || fallback.Err != nil || sortedMatchingPaths(native.Text) != tc.want ||
			sortedMatchingPaths(fallback.Text) != tc.want {
			t.Fatalf("%s: native=%+v fallback=%+v err=%v", tc.args, native, fallback, err)
		}
	}
}

func sortedMatchingPaths(text string) string {
	lines := strings.Split(text, "\n")
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestMatchingFilesGlobalLimitCountsUniqueFiles(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 4; i++ {
		writeSearchFixture(t, root, fmt.Sprintf("src/f%d.go", i), strings.Repeat("needle\n", 500))
	}
	tool := NewSearchCode(root)
	for _, backend := range []string{"fallback", "rg"} {
		if backend == "rg" && os.Getenv("SUPERCLI_TEST_RG") == "" {
			continue
		}
		preview := &searchContext{filesOnly: true}
		var result Result
		var err error
		if backend == "rg" {
			result, err = tool.ripgrep(context.Background(), os.Getenv("SUPERCLI_TEST_RG"), root, "needle", 2, preview)
		} else {
			result, err = tool.fallback(context.Background(), root, "needle", 2, preview)
		}
		lines := strings.Split(result.Text, "\n")
		if err != nil || result.Err != nil || len(lines) != 3 || lines[0] == lines[1] ||
			!strings.Contains(lines[2], "2 matching files") || !strings.Contains(lines[2], "results may be incomplete") ||
			len(preview.records) != 0 || len(preview.hits) != 0 {
			t.Fatalf("%s: %+v %v", backend, result, err)
		}
	}
}

func TestMatchingFilesValidationCancellationAndExternalRoot(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	home := filepath.Join(root, "workspace")
	external := filepath.Join(root, "shared")
	writeSearchFixture(t, home, "inside.go", "needle\n")
	externalFile := writeSearchFixture(t, external, "outside.go", "needle\n")
	tool := NewSearchCode(home)
	for _, args := range []string{
		`{"query":"needle","output_mode":"unknown"}`,
		`{"query":"needle","output_mode":"files","include":"../*.go"}`,
		`{"query":"needle","output_mode":"files","path":"missing"}`,
	} {
		result, err := tool.run(context.Background(), json.RawMessage(args))
		if err != nil || result.Err == nil || result.Text == "no matches" {
			t.Fatalf("validation hidden: %s %+v %v", args, result, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := tool.run(ctx, json.RawMessage(`{"query":"needle","output_mode":"files"}`))
	if err != nil || !errors.Is(result.Err, context.Canceled) || result.Text == "no matches" {
		t.Fatalf("cancellation hidden: %+v %v", result, err)
	}
	args, _ := json.Marshal(map[string]any{"query": "needle", "output_mode": "files", "path": external})
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(false) })
	denied, err := tool.run(context.Background(), args)
	if err != nil || denied.Err == nil {
		t.Fatal("sandbox bypassed")
	}
	sandbox.SetUnsandboxed(true)
	allowed, err := tool.run(context.Background(), args)
	canonical, canonErr := filepath.EvalSymlinks(externalFile)
	if err != nil || allowed.Err != nil || canonErr != nil || allowed.Text != canonical ||
		!filepath.IsAbs(allowed.Text) {
		t.Fatalf("external path changed: %+v %v", allowed, err)
	}
}

func TestMatchingFilesRGFailureAndTimeoutPreserveMeaning(t *testing.T) {
	root := t.TempDir()
	writeSearchFixture(t, root, "answer.go", "needle\n")
	tool := NewSearchCode(root)
	t.Setenv("SUPERCLI_SEARCH_RG_HELPER", "fail")
	result, err := tool.ripgrep(context.Background(), os.Args[0], root, "needle", 50, &searchContext{filesOnly: true})
	if err != nil || result.Err != nil || result.Text != "answer.go" {
		t.Fatalf("fallback lost answer: %+v %v", result, err)
	}
	failed, failErr := tool.ripgrep(context.Background(), os.Args[0], filepath.Join(root, "missing"), "needle", 50, &searchContext{filesOnly: true})
	if failErr != nil || failed.Err == nil || failed.Text == "no matches" || !strings.Contains(failed.Err.Error(), "search_failed") {
		t.Fatalf("both backend failures hidden: %+v %v", failed, failErr)
	}
	t.Setenv("SUPERCLI_SEARCH_RG_HELPER", "cancel")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	result, err = tool.ripgrep(ctx, os.Args[0], root, "needle", 50, &searchContext{filesOnly: true})
	if err != nil || !errors.Is(result.Err, context.DeadlineExceeded) || result.Text == "no matches" {
		t.Fatalf("timeout hidden: %+v %v", result, err)
	}
}

func TestMatchingFilesCommandBoundsFirstHitAndContent(t *testing.T) {
	cmd := NewSearchCode(".").ripgrepCommand(context.Background(), "rg", ".", "needle", 50, &searchContext{filesOnly: true})
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--max-count 1") || !strings.Contains(args, "--max-columns 1") ||
		!strings.Contains(args, "--null") || strings.Contains(args, "--files-with-matches") {
		t.Fatalf("matching-file command: %s", args)
	}
	lines := NewSearchCode(".").ripgrepCommand(context.Background(), "rg", ".", "needle", 50, &searchContext{})
	if strings.Contains(strings.Join(lines.Args, " "), "--max-columns") {
		t.Fatal("line-search command changed")
	}
}

func BenchmarkMatchingFilesStopsEarly(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 32; i++ {
		writeSearchFixture(b, root, fmt.Sprintf("src/file%02d.go", i), "needle\n"+strings.Repeat("unrelated long source line for scanning\n", 8192))
	}
	tool := NewSearchCode(root)
	for _, mode := range []string{"lines", "files"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				result, err := tool.fallback(context.Background(), root, "needle", 50, &searchContext{filesOnly: mode == "files"})
				if err != nil || result.Err != nil || strings.Contains(result.Text, "limit reached") {
					b.Fatalf("%+v %v", result, err)
				}
				b.ReportMetric(float64(len(result.Text)), "output-B")
			}
		})
	}
}

func init() {
	if os.Getenv("SUPERCLI_SEARCH_FILES_HELPER") == "" {
		return
	}
	args := strings.Join(os.Args[1:], " ")
	if !strings.Contains(args, "--max-count 1") || !strings.Contains(args, "--max-columns 1") {
		fmt.Fprintln(os.Stderr, "missing first-hit command flags")
		os.Exit(2)
	}
	root := os.Args[len(os.Args)-1]
	// A duplicate record must not consume another slot in the file cap.
	fmt.Printf("%s/a.go\x001:first\n%s/a.go\x002:duplicate\n%s/b.go\x001:second\n", root, root, root)
	os.Exit(0)
}

func TestMatchingFilesDuplicateRecordsDoNotConsumeLimit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SUPERCLI_SEARCH_FILES_HELPER", "duplicates")
	result, err := NewSearchCode(root).ripgrep(context.Background(), os.Args[0], root, "needle", 2, &searchContext{filesOnly: true})
	rows := strings.Split(result.Text, "\n")
	if err != nil || result.Err != nil || len(rows) != 3 ||
		rows[0] != "a.go" || rows[1] != "b.go" || !strings.Contains(rows[2], "2 matching files") {
		t.Fatalf("duplicate spent a file slot: %+v %v", result, err)
	}
}
