package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

func TestWorkerReportPreviewPreservesMiddleFindings(t *testing.T) {
	report := "# Review\n\n## Verdict\nNeeds changes.\n\n## Flow\n" +
		strings.Repeat("Already understood architecture and call sites.\n", 250)
	for _, title := range []string{"### [P1] Wrong discovery interval", "### [P1] Unreachable transport", "### [P2] Busy retry loop"} {
		report += "\n" + title + "\n" + strings.Repeat("Detailed code evidence.\n", 120)
	}
	report += "\n## Verification\nChecks were NOT run; two-device test remains required.\n"
	w := &Worker{ID: "worker-7", Agent: "review", Status: "done"}
	result := workerResult(w, report, nil)
	if result.Text != renderWorkerNotification(w, report) {
		t.Fatal("UI report was rewritten")
	}
	reg := tools.NewRegistry()
	reg.EnsureReadOutput()
	visible := reg.ModelResultContent("task", result)
	for _, marker := range []string{"worker-7", "<status>done</status>", "Needs changes.", "[P1] Wrong discovery interval", "[P1] Unreachable transport", "[P2] Busy retry loop", "Checks were NOT run"} {
		if !strings.Contains(visible, marker) {
			t.Errorf("handoff lost %q", marker)
		}
	}
	if len(result.ModelPreview) > core.ModelOutputPreviewBytes || len(visible) > core.ModelOutputPreviewBytes+256 || !utf8.ValidString(visible) {
		t.Fatalf("preview exceeded existing budget: %d/%d bytes", len(result.ModelPreview), len(visible))
	}
	handle := handleInOutput(visible)
	if handle == "" {
		t.Fatal("full report was not retained")
	}
	tool, _ := reg.Get("read_output")
	got, err := tool.Fn(t.Context(), []byte("{\"handle\":\""+handle+"\",\"query\":\"Detailed code evidence\"}"))
	if err != nil || got.Err != nil || !strings.Contains(got.Text, "Detailed code evidence") {
		t.Fatalf("full middle evidence unavailable: %v %+v", err, got)
	}
}

func TestWorkerReportPreviewLeavesSmallPlainAndFailedResults(t *testing.T) {
	for _, tc := range []struct {
		name, report string
		err          error
	}{
		{"small", "## Findings\nOne issue.\n## Checks\nPassed.", nil},
		{"plain", strings.Repeat("plain output ", 1000), nil},
		{"failed", "# Review\n## Findings\n" + strings.Repeat("partial ", 2000) + "\n## Checks\nIncomplete.", errors.New("interrupted")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &Worker{ID: "worker-2", Agent: "review", Status: "done"}
			if tc.err != nil {
				w.Status = "failed"
			}
			result := workerResult(w, tc.report, tc.err)
			if result.ModelPreview != "" {
				t.Fatal("unnecessary structured preview")
			}
			if tc.err != nil && !errors.Is(result.Err, tc.err) {
				t.Fatal("lost failure cause")
			}
		})
	}
}

func TestWorkerReportHeadingsIgnoresCodeExamples(t *testing.T) {
	report := "# Review\r\n" +
		"    ## Indented example\n\t## Tab example\n" +
		"\x60\x60\x60\x60markdown\n## Fake heading\n\x60\x60\x60\n## Still fenced\n\x60\x60\x60\x60\n" +
		"~~~text\n### Tilde example\n~~~\n" +
		"###RealNotAHeading\n####### Too deep\n### \n" +
		"  ## Real finding\n###\tChecks\n"
	outline, count := workerReportHeadings(report)
	if count != 3 || outline != "# Review\n  ## Real finding\n###\tChecks\n" {
		t.Fatalf("unexpected outline (%d): %q", count, outline)
	}
}

func TestWorkerReportPreviewBoundsUnicodeAndManyHeadings(t *testing.T) {
	report := "# Przegląd\n" + strings.Repeat("### Ustalenie źółć 😀\nOpis wyniku.\n", 3000) +
		"## " + strings.Repeat("ż", 20000) + "\n## Weryfikacja\nNie wykonano testów.\n"
	w := &Worker{ID: "worker-8", Agent: "review", Status: "done"}
	result := workerResult(w, report, nil)
	if result.ModelPreview == "" || len(result.ModelPreview) > core.ModelOutputPreviewBytes || !utf8.ValidString(result.ModelPreview) {
		t.Fatalf("invalid preview: %d bytes", len(result.ModelPreview))
	}
	for _, marker := range []string{"omitted_bytes=", "## Weryfikacja", "Nie wykonano testów."} {
		if !strings.Contains(result.ModelPreview, marker) {
			t.Errorf("missing %q", marker)
		}
	}
}

func TestWorkerReportPreviewPreservesObservations(t *testing.T) {
	report := "# Review\n## Findings\n" + strings.Repeat("Detailed finding.\n", 800) + "## Checks\nIncomplete.\n"
	for _, large := range []bool{false, true} {
		t.Run(fmt.Sprint(large), func(t *testing.T) {
			evidence := "== ctx_execute ==\nTEST FAILURE: retry remains required"
			if large {
				evidence += strings.Repeat("\nDetailed failure output.", 200)
			}
			w := &Worker{ID: "worker-9", Agent: "review", Status: "done", lastEvidence: evidence}
			result := workerResult(w, report, nil)
			if result.ModelPreview == "" || !strings.Contains(result.ModelPreview, "historical snapshots") {
				t.Fatal("observation metadata lost from preview")
			}
			if !large && !strings.Contains(result.ModelPreview, "TEST FAILURE") {
				t.Fatal("inline failure lost from preview")
			}
			reg := tools.NewRegistry()
			reg.EnsureReadOutput()
			visible := reg.ModelResultContent("task", result)
			handle := handleInOutput(visible)
			tool, _ := reg.Get("read_output")
			got, err := tool.Fn(t.Context(), []byte("{\"handle\":\""+handle+"\",\"query\":\"TEST FAILURE\"}"))
			if err != nil || got.Err != nil || !strings.Contains(got.Text, "TEST FAILURE") {
				t.Fatalf("full observations unavailable: %v %+v", err, got)
			}
		})
	}
}

// Opt-in replay of a private saved report. No provider call or live-session read.
func TestWorkerReportPreviewReplay(t *testing.T) {
	path := os.Getenv("SUPERCLI_TEST_WORKER_REPORT")
	if path == "" {
		t.Skip("set SUPERCLI_TEST_WORKER_REPORT to a saved Markdown report")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	report := string(data)
	w := &Worker{ID: "worker-2", Agent: "review", Status: "done"}
	result := workerResult(w, report, nil)
	reg := tools.NewRegistry()
	reg.EnsureReadOutput()
	after := reg.ModelResultContent("task", result)
	result.ModelPreview = ""
	before := reg.ModelResultContent("task", result)
	headings := regexp.MustCompile("(?m)^### [0-9]+[.] [^\r\n]+").FindAllString(report, -1)
	beforeCount, afterCount := 0, 0
	for _, heading := range headings {
		if strings.Contains(before, heading) {
			beforeCount++
		}
		if strings.Contains(after, heading) {
			afterCount++
		}
	}
	if len(headings) == 0 || afterCount != len(headings) || afterCount <= beforeCount {
		t.Fatalf("finding headings retained: before=%d after=%d total=%d", beforeCount, afterCount, len(headings))
	}
	dir := filepath.Dir(path)
	for name, text := range map[string]string{"before-preview.txt": before, "after-preview.txt": after} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	metrics, _ := json.MarshalIndent(map[string]int{
		"report_bytes": len(data), "before_preview_bytes": len(before), "after_preview_bytes": len(after),
		"numbered_findings": len(headings), "before_findings": beforeCount, "after_findings": afterCount,
	}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "replay-metrics.json"), metrics, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log(string(metrics))
}
