package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"supercli/internal/tools/sandbox"
)

// A marked child acts as rg and reports a captured hit in a file that is already
// gone. This exercises the actual run's search-to-context failure without a
// timing race, sleep, or a production hook. Unmarked test processes are unchanged.
func init() {
	if os.Getenv("SUPERCLI_SEARCH_ASSEMBLY_MISSING_RG") != "1" {
		return
	}
	if len(os.Args) < 2 || !strings.Contains(strings.Join(os.Args[1:], " "), "--null") {
		os.Exit(2)
	}
	root := os.Args[len(os.Args)-1]
	path := filepath.Join(root, filepath.FromSlash(strings.Repeat("project/", 5)+"limits/gone.txt"))
	for line := 1; line <= 20; line++ {
		fmt.Printf("%s\x00%d:BudgetLimit=7\n", path, line)
	}
	os.Exit(0)
}

// Preserve the pre-change tail as a parity reference. This is intentionally not
// a benchmark entry point: both performance variants must benchmark real run,
// with root applying search-context-before-n.json as a baseline source overlay.
func legacySearchContextAssembly(ctx context.Context, tool *SearchCode, result Result, preview *searchContext, auto, clamped bool, runErr error) (Result, error) {
	if runErr == nil {
		result = tool.previewSearchHits(result, preview, preview.query)
	}
	if runErr == nil && result.Err == nil && preview.radius > 0 {
		if !auto || (len(preview.hits) <= 3 && len(preview.longLines) == 0) {
			expanded := tool.renderSearchContext(ctx, preview, result)
			if !auto || len(expanded.Text) <= 2048 || expanded.Err != nil {
				result = expanded
			}
		}
	}
	if clamped && runErr == nil && result.Err == nil {
		result.Text += fmt.Sprintf("\n[context limited to %d lines per match]", maxSearchContextRadius)
	}
	return result, runErr
}

// Only valid content-query fixture arguments are accepted here. The common
// backend and canonical paths are real production code; the reference freezes
// only the old preview-before-context assembly, rather than another whole run.
func legacySearchContextFixtureRun(tb testing.TB, ctx context.Context, tool *SearchCode, raw json.RawMessage) (Result, error) {
	tb.Helper()
	var args searchCodeArgs
	if err := json.Unmarshal(raw, &args); err != nil || args.Query == "" || args.OutputMode != "" {
		tb.Fatalf("invalid assembly fixture arguments: %v", err)
	}
	root, err := sandbox.ResolveSafe(tool.WorkDir, args.Path)
	if err != nil {
		tb.Fatal(err)
	}
	local := *tool
	if base, absErr := filepath.Abs(tool.WorkDir); absErr == nil {
		if canonical, resolveErr := filepath.EvalSymlinks(base); resolveErr == nil {
			local.WorkDir = canonical
		}
	}
	tool = &local
	radius := 4
	auto := args.Context == nil
	if !auto {
		radius = *args.Context
	}
	if radius < 0 {
		tb.Fatal("negative assembly fixture radius")
	}
	clamped := radius > maxSearchContextRadius
	radius = min(radius, maxSearchContextRadius)
	if args.Max <= 0 {
		args.Max = 50
	}
	include, err := compileSearchGlob(args.Include)
	if err != nil {
		tb.Fatal(err)
	}
	preview := &searchContext{radius: radius, include: include, query: args.Query}
	var result Result
	if rg := tool.rgPath(); rg != "" {
		result, err = tool.ripgrep(ctx, rg, root, args.Query, args.Max, preview)
	} else {
		result, err = tool.fallback(ctx, root, args.Query, args.Max, preview)
	}
	return legacySearchContextAssembly(ctx, tool, result, preview, auto, clamped, err)
}

func requireSearchAssemblyParity(tb testing.TB, got, want Result, gotErr, wantErr error) {
	tb.Helper()
	// DeepEqual covers every Result field, including the model projection,
	// retained evidence, inert flags, native blocks and error-chain structure.
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotErr, wantErr) {
		tb.Fatalf("full search Result differs: text=%d/%d retained=%d/%d modelText=%d/%d modelPreview=%d/%d err=%v/%v returnErr=%v/%v",
			len(got.Text), len(want.Text), len(got.RetainedText), len(want.RetainedText), len(got.ModelText), len(want.ModelText), len(got.ModelPreview), len(want.ModelPreview), got.Err, want.Err, gotErr, wantErr)
	}
}

func searchAssemblyArgs(tb testing.TB, radius *int) json.RawMessage {
	tb.Helper()
	args := map[string]any{"query": "BudgetLimit", "max": 50}
	if radius != nil {
		args["context"] = *radius
	}
	raw, err := json.Marshal(args)
	if err != nil {
		tb.Fatal(err)
	}
	return raw
}

func searchAssemblyFixture(tb testing.TB, kind string) string {
	tb.Helper()
	if kind == "grouped" {
		root, _ := groupedSearchFixture(tb)
		return root
	}
	root := tb.TempDir()
	switch kind {
	case "minified":
		var body strings.Builder
		for i := 0; i < 6; i++ {
			fmt.Fprintf(&body, "%s BudgetLimit_%02d=%d\r\n", strings.Repeat("ą", 5000), i, 7000+i)
		}
		writeSearchFixture(tb, root, "minified.txt", body.String())
	case "cap":
		for i := 0; i < 24; i++ {
			body := strings.Repeat("before\n", 20) + fmt.Sprintf("BudgetLimit_%02d=zażółć\n", i) + strings.Repeat("after\n", 20)
			writeSearchFixture(tb, root, fmt.Sprintf("f%02d.txt", i), body)
		}
	case "sparse":
		writeSearchFixture(tb, root, "one.txt", "before\nBudgetLimit=7\nanswer=235\nafter\n")
	case "auto-large":
		writeSearchFixture(tb, root, "one.txt", "BudgetLimit=7\n"+strings.Repeat("ą", 3000)+"\nafter\n")
	case "no-hits":
		writeSearchFixture(tb, root, "one.txt", "ordinary\ncontent\n")
	default:
		tb.Fatalf("unknown assembly fixture %q", kind)
	}
	return root
}

func TestSearchContextRunAssemblyFullParity(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	zero, one, twenty, clamped := 0, 1, 20, 99
	for _, kind := range []string{"grouped", "minified", "cap", "sparse", "auto-large", "no-hits"} {
		t.Run(kind, func(t *testing.T) {
			root := searchAssemblyFixture(t, kind)
			tool := NewSearchCode(root)
			if tool.rgPath() != "" {
				t.Fatal("fixture must exercise the Go backend")
			}
			for _, mode := range []struct {
				name   string
				radius *int
			}{
				{"locations", &zero}, {"auto", nil}, {"context", &one}, {"wide", &twenty}, {"clamped", &clamped},
			} {
				t.Run(mode.name, func(t *testing.T) {
					args := searchAssemblyArgs(t, mode.radius)
					want, wantErr := legacySearchContextFixtureRun(t, context.Background(), tool, args)
					got, gotErr := tool.run(context.Background(), args)
					requireSearchAssemblyParity(t, got, want, gotErr, wantErr)
					if got.Err != nil || gotErr != nil {
						t.Fatalf("synthetic run failed: %v/%v", got.Err, gotErr)
					}
					if kind == "grouped" && (mode.name == "locations" || mode.name == "auto") && got.ModelText == "" {
						t.Fatal("control no longer uses complete grouped model text")
					}
					if kind == "cap" && (mode.name == "wide" || mode.name == "clamped") && (!strings.Contains(got.Text, "context capped at 500 lines") || got.RetainedText == "") {
						t.Fatal("cap fixture must exercise retained locations and context")
					}
				})
			}
		})
	}
}

func TestSearchContextAssemblyFailuresPreserveFullResult(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	one := 1
	args := searchAssemblyArgs(t, &one)
	root := searchAssemblyFixture(t, "grouped")
	tool := NewSearchCode(root)
	preview := &searchContext{radius: one, query: "BudgetLimit"}
	locations, err := tool.fallback(context.Background(), root, preview.query, 50, preview)
	if err != nil || locations.Err != nil || len(preview.hits) != 20 {
		t.Fatalf("capture failed: %v/%v hits=%d", err, locations.Err, len(preview.hits))
	}
	if projected := tool.previewSearchHits(locations, preview, preview.query); projected.ModelText == "" {
		t.Fatal("fixture does not trigger discarded preview")
	}
	t.Run("after-capture-cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		want, wantErr := legacySearchContextAssembly(ctx, tool, locations, preview, false, false, nil)
		got := tool.renderSearchContext(ctx, preview, locations)
		requireSearchAssemblyParity(t, got, want, nil, wantErr)
		if !errors.Is(got.Err, context.Canceled) {
			t.Fatal("late cancellation was hidden")
		}
		want, wantErr = legacySearchContextFixtureRun(t, ctx, tool, args)
		got, gotErr := tool.run(ctx, args)
		requireSearchAssemblyParity(t, got, want, gotErr, wantErr)
		if !errors.Is(got.Err, context.Canceled) {
			t.Fatal("actual run cancellation was hidden")
		}
	})
	t.Run("disappeared-after-capture", func(t *testing.T) {
		if err := os.Remove(preview.hits[0].path); err != nil {
			t.Fatal(err)
		}
		want, wantErr := legacySearchContextAssembly(context.Background(), tool, locations, preview, false, false, nil)
		got := tool.renderSearchContext(context.Background(), preview, locations)
		requireSearchAssemblyParity(t, got, want, nil, wantErr)
		if !errors.Is(got.Err, os.ErrNotExist) || got.Text != locations.Text {
			t.Fatal("captured locations or read failure were lost")
		}
	})
}

func searchAssemblyMissingRG(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	name := "rg"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dest := filepath.Join(bin, name)
	// Windows locks every hard link to the running test executable. Copy its
	// image so the marked child can exit and TempDir can remove the fixture.
	if runtime.GOOS != "windows" {
		if err := os.Link(os.Args[0], dest); err == nil {
			return bin
		}
	}
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	output, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(output, source)
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("synthetic rg fixture copy: %v/%v", copyErr, closeErr)
	}
	return bin
}

func TestSearchContextRunReadFailureFullParity(t *testing.T) {
	t.Setenv("PATH", searchAssemblyMissingRG(t))
	t.Setenv("SUPERCLI_SEARCH_ASSEMBLY_MISSING_RG", "1")
	root := t.TempDir()
	tool := NewSearchCode(root)
	one := 1
	args := searchAssemblyArgs(t, &one)
	want, wantErr := legacySearchContextFixtureRun(t, context.Background(), tool, args)
	got, gotErr := tool.run(context.Background(), args)
	requireSearchAssemblyParity(t, got, want, gotErr, wantErr)
	if !errors.Is(got.Err, os.ErrNotExist) || !strings.Contains(got.Text, "gone.txt:1:BudgetLimit=7") || got.ModelText != "" || got.ModelPreview != "" {
		t.Fatalf("actual run lost captured locations or read failure: %v", got.Err)
	}
}

var searchContextAssemblyBenchmarkResult Result

// Root runs this exact benchmark against the saved original source overlay and
// the candidate. Every timed iteration calls actual run, including JSON decode,
// sandbox/canonical path resolution, search, file reads and final assembly. The
// legacy parity reference above is never used for timing.
func BenchmarkSearchContextRunAssembly(b *testing.B) {
	b.Setenv("PATH", b.TempDir())
	zero, one, twenty := 0, 1, 20
	for _, scenario := range []struct {
		kind, mode string
		radius     *int
	}{
		{"grouped", "locations", &zero}, {"grouped", "auto", nil}, {"grouped", "context", &one},
		{"minified", "locations", &zero}, {"minified", "auto", nil}, {"minified", "context", &one},
		{"cap", "wide", &twenty}, {"sparse", "context", &one},
	} {
		b.Run(scenario.kind+"/"+scenario.mode, func(b *testing.B) {
			tool := NewSearchCode(searchAssemblyFixture(b, scenario.kind))
			if tool.rgPath() != "" {
				b.Fatal("fixture must exercise the Go backend")
			}
			args := searchAssemblyArgs(b, scenario.radius)
			initial, err := tool.run(context.Background(), args)
			if err != nil || initial.Err != nil {
				b.Fatal(err, initial.Err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				result, err := tool.run(context.Background(), args)
				if err != nil || result.Err != nil {
					b.Fatal(err, result.Err)
				}
				searchContextAssemblyBenchmarkResult = result
			}
			b.ReportMetric(float64(len(initial.Text)), "text-B")
			b.ReportMetric(float64(len(initial.RetainedText)), "retained-B")
		})
	}
}
