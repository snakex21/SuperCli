package uitest

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These intentionally tiny, native ESM fixtures exercise the actual Go runner
// independently of the production UI harness, including imports and failures.
const runnerFixtureHarness = `export async function run(files) {
	const response = await fetch('/__test/sources.json');
	if (!response.ok) throw new Error('snapshot request failed');
	const sources = await response.json();
	const result = {total: 0, passed: 0, skipped: 0, tests: [], failures: []};
	for (const file of files) {
		const start = performance.now();
		try {
			const module = await import('/' + file);
			await module.run(sources);
			result.passed++;
			result.tests.push({name: file, status: 'passed', duration_ms: performance.now() - start});
		} catch (error) {
			result.tests.push({name: file, status: 'failed', duration_ms: performance.now() - start});
			result.failures.push({name: file, error: String(error), stack: error.stack || ''});
		}
		result.total++;
	}
	return result;
}`

func TestRunnerNativeESMEndToEnd(t *testing.T) {
	root := t.TempDir()
	putSource(t, root, "test/ui/harness.js", runnerFixtureHarness)
	putSource(t, root, "internal/webgui/assets/untracked.js", "export const answer = 42;")
	putSource(t, root, "test/ui/a-native.test.js", `import {answer} from '/internal/webgui/assets/untracked.js';
		export async function run(sources) {
			const element = document.createElement('button');
			let clicked = false; element.addEventListener('click', () => { clicked = true; });
			document.body.append(element); element.click(); element.remove();
			await Promise.resolve();
			if (!clicked || !(element instanceof HTMLButtonElement) || answer !== 42) throw Error('not native DOM/ESM');
			if (!sources['internal/webgui/assets/untracked.js']) throw Error('untracked source missing');
			if (sources['.supercli/auth.json']) throw Error('private data exposed');
		}`)
	putSource(t, root, "test/ui/harness.test.js", `export function run() {
		if (typeof gc !== 'function' || !performance.memory) throw Error('test memory flags missing');
	}`)
	putSource(t, root, "test/ui/z-last.test.js", "export async function run() { await Promise.resolve(); }")
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	result, err := Run(ctx, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 3 || result.Passed != 3 || result.Skipped != 0 || len(result.Failures) != 0 {
		t.Fatalf("unexpected native runner result: %+v", result)
	}
	var names []string
	for _, test := range result.Tests {
		names = append(names, test.Name)
	}
	if !reflect.DeepEqual(names, []string{"test/ui/a-native.test.js", "test/ui/harness.test.js", "test/ui/z-last.test.js"}) {
		t.Fatalf("runner did not execute sorted files including harness.test.js: %v", names)
	}
	result.Log(t.Logf)
	filtered, err := Run(ctx, Options{Root: root, Filter: "z-last"})
	if err != nil || filtered.Total != 1 || filtered.Tests[0].Name != "test/ui/z-last.test.js" {
		t.Fatalf("filtered native runner: %+v %v", filtered, err)
	}
	putSource(t, root, "test/ui/failure.test.js", "export function run() { throw new Error('deliberate native runner failure'); }")
	failed, err := Run(ctx, Options{Root: root, Filter: "failure"})
	if err == nil || failed.Total != 1 || failed.Passed != 0 || len(failed.Failures) != 1 || failed.Tests[0].Status != "failed" || !strings.Contains(failed.Failures[0].Stack, "failure.test.js") {
		t.Fatalf("runner concealed native failure/stack: %+v %v", failed, err)
	}
	var log strings.Builder
	failed.Log(func(format string, args ...any) { fmt.Fprintf(&log, format+"\n", args...) })
	if !strings.Contains(log.String(), "deliberate native runner failure") || !strings.Contains(log.String(), "[failed]") || !strings.Contains(log.String(), "failed=1") {
		t.Fatalf("failure logging incomplete: %s", log.String())
	}
}

func TestResultValidationAndSkipLogging(t *testing.T) {
	valid := Result{Total: 2, Passed: 1, Skipped: 1, Tests: []Test{{Name: "pass", Status: "passed"}, {Name: "explicit skip", Status: "skipped"}}}
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	valid.Log(func(format string, args ...any) { fmt.Fprintf(&log, format+"\n", args...) })
	if !strings.Contains(log.String(), "[skipped] explicit skip") || !strings.Contains(log.String(), "skipped=1") {
		t.Fatal("skip was hidden")
	}
	for _, result := range []Result{
		{},
		{Total: 2, Passed: 2, Tests: []Test{{Name: "only one", Status: "passed"}}},
		{Total: 1, Passed: 1, Tests: []Test{{Name: "failed", Status: "failed"}}},
		{Total: 1, Passed: 1, Tests: []Test{{Name: "unknown", Status: "made-up"}}},
		{Total: 1, Tests: []Test{{Name: "failed", Status: "failed"}}, Failures: []Failure{{Name: "failed", Error: "failure", Stack: "stack"}}},
		{Total: 1, Passed: 1, Tests: []Test{{Name: "", Status: "passed"}}},
		{Total: 1, Passed: 1, Tests: []Test{{Name: "negative duration", Status: "passed", DurationMS: -1}}},
	} {
		if err := result.validate(); err == nil {
			t.Errorf("accepted invalid/failing result: %+v", result)
		}
	}
}

func TestRunnerRejectsEmptySuiteAndMissingBrowser(t *testing.T) {
	root := t.TempDir()
	putSource(t, root, "test/ui/harness.js", runnerFixtureHarness)
	putSource(t, root, "test/ui/only.test.js", "export function run() {}")
	if _, err := Run(t.Context(), Options{Root: root, Filter: "no-match"}); err == nil || !strings.Contains(err.Error(), "no test/ui/*.test.js") {
		t.Fatalf("empty filter was hidden: %v", err)
	}
	if _, err := Run(t.Context(), Options{Root: root, Browser: root + "/missing-browser"}); err == nil || !strings.Contains(err.Error(), "dev-only") || !strings.Contains(err.Error(), "SUPERCLI_TEST_BROWSER") {
		t.Fatalf("missing browser was not an actionable error: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Run(ctx, Options{Root: root}); err == nil {
		t.Fatal("ignored runner cancellation")
	}
}
