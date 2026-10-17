package uitest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"supercli/internal/testbrowser"
)

type Options struct {
	Root    string // optional repo root; otherwise found from the working directory
	Filter  string // case-sensitive substring of a test file's basename
	Browser string // optional Chrome/Chromium/Edge executable
}

type Test struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	DurationMS float64 `json:"duration_ms"`
}

type Failure struct {
	Name  string `json:"name"`
	Error string `json:"error"`
	Stack string `json:"stack"`
}

type Result struct {
	Total    int       `json:"total"`
	Passed   int       `json:"passed"`
	Skipped  int       `json:"skipped"`
	Tests    []Test    `json:"tests"`
	Failures []Failure `json:"failures"`
}

func findRoot(root string) (string, error) {
	if root != "" {
		return filepath.Abs(root)
	}
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if info, err := os.Stat(filepath.Join(current, "go.mod")); err == nil && info.Mode().IsRegular() {
			if info, err := os.Stat(filepath.Join(current, "test", "ui")); err == nil && info.IsDir() {
				return current, nil
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("UI test repo root not found (run from the SuperCli checkout)")
		}
		current = parent
	}
}

func testFiles(sources map[string]string, filter string) ([]string, error) {
	var files []string
	for name := range sources {
		rest, ok := strings.CutPrefix(name, "test/ui/")
		if ok && !strings.Contains(rest, "/") && strings.HasSuffix(rest, ".test.js") && strings.Contains(path.Base(name), filter) {
			files = append(files, name)
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no test/ui/*.test.js files match filter %q", filter)
	}
	return files, nil
}

func (r Result) validate() error {
	if r.Total <= 0 || r.Total != len(r.Tests) || r.Passed < 0 || r.Skipped < 0 {
		return fmt.Errorf("invalid UI harness counts: total=%d passed=%d skipped=%d tests=%d", r.Total, r.Passed, r.Skipped, len(r.Tests))
	}
	counts := map[string]int{}
	for _, test := range r.Tests {
		if test.Name == "" || test.DurationMS < 0 {
			return fmt.Errorf("invalid UI test record: %+v", test)
		}
		switch test.Status {
		case "passed", "failed", "skipped":
			counts[test.Status]++
		default:
			return fmt.Errorf("unknown UI test status %q for %q", test.Status, test.Name)
		}
	}
	if counts["passed"] != r.Passed || counts["skipped"] != r.Skipped || counts["failed"] != len(r.Failures) {
		return fmt.Errorf("UI harness summary disagrees with individual results: passed=%d/%d skipped=%d/%d failed=%d/%d", r.Passed, counts["passed"], r.Skipped, counts["skipped"], len(r.Failures), counts["failed"])
	}
	for _, failure := range r.Failures {
		if failure.Name == "" || (failure.Error == "" && failure.Stack == "") {
			return errors.New("UI harness returned an empty failure record")
		}
	}
	if len(r.Failures) > 0 {
		return fmt.Errorf("UI tests failed: %d of %d (passed=%d, skipped=%d)", len(r.Failures), r.Total, r.Passed, r.Skipped)
	}
	return nil
}

// Run uses one browser for the entire suite. Failures remain available in Result
// even when err is non-nil; neither callers nor the runner silently skip Chrome.
func Run(ctx context.Context, opts Options) (result Result, err error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	root, err := findRoot(opts.Root)
	if err != nil {
		return result, err
	}
	sources, err := snapshot(root)
	if err != nil {
		return result, err
	}
	files, err := testFiles(sources, opts.Filter)
	if err != nil {
		return result, err
	}
	if _, ok := sources["test/ui/harness.js"]; !ok {
		return result, errors.New("native ESM UI harness missing: test/ui/harness.js")
	}
	server, err := serveSnapshot(sources)
	if err != nil {
		return result, err
	}
	defer server.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Executable: opts.Browser})
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, browser.Close()) }()
	page, err := browser.NewPage(ctx, server.URL+"/")
	if err != nil {
		return result, err
	}
	encoded, err := json.Marshal(files)
	if err != nil {
		return result, err
	}
	expression := "import('/test/ui/harness.js').then(h => h.run(" + string(encoded) + "))"
	if err := page.Eval(ctx, expression, &result); err != nil {
		return result, fmt.Errorf("native ESM UI harness: %w", err)
	}
	return result, result.validate()
}

// Log emits every test and every failure, including explicit skipped records.
// The callback works with testing.T.Logf as well as a CLI line printer.
func (r Result) Log(logf func(string, ...any)) {
	for _, test := range r.Tests {
		logf("[%s] %s (%.2f ms)", test.Status, test.Name, test.DurationMS)
	}
	for _, failure := range r.Failures {
		logf("FAIL %s: %s\n%s", failure.Name, failure.Error, failure.Stack)
	}
	logf("UI: total=%d passed=%d skipped=%d failed=%d", r.Total, r.Passed, r.Skipped, len(r.Failures))
}
