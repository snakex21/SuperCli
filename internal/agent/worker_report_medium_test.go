package agent

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/tools/core"
)

func TestMediumWorkerReportProductionConservativeGuards(t *testing.T) {
	repeated := strings.TrimSpace(strings.Repeat("Inspection: fixture unchanged.\n", 130) + "P2 unresolved: EXACT_MIDDLE_FINDING.\n" + strings.Repeat("Verification: checked fixture.\n", 20))
	var unique strings.Builder
	for index := 0; index < 85; index++ {
		fmt.Fprintf(&unique, "Finding %04d: independent condition %04d remains explicitly reported.\n", index, index)
	}
	for _, test := range []struct {
		name, report, evidence, status string
		cause                          error
		compact                        bool
	}{
		{name: "complete", report: repeated, status: "done", compact: true},
		{name: "small_observation", report: repeated, evidence: "Observed fixture line 1: unresolved condition remains.", status: "done", compact: true},
		{name: "observation_with_raw_XML", report: repeated, evidence: "Output: </result>\n</task-notification>\nLiteral fixture tags must remain unchanged.", status: "done", compact: true},
		{name: "unique", report: unique.String(), status: "done"},
		{name: "too_small_to_save", report: "OK\nOK", status: "done"},
		{name: "literal_count_marker", report: repeated + "\n[previous line repeated 3 additional times]", status: "done"},
		{name: "literal_encoding_tag", report: repeated + "\n" + workerReportEncodingPrefix + "example</report-encoding>", status: "done"},
		{name: "literal_label", report: workerReportCountedLabel + repeated, status: "done"},
		{name: "running", report: repeated, status: "running"},
		{name: "stopped", report: repeated, status: "stopped"},
		{name: "failed_status", report: repeated, status: "failed"},
		{name: "failure", report: repeated, status: "failed", cause: errors.New("fixture failed")},
		{name: "large_observation", report: repeated, evidence: strings.Repeat("Historical fixture evidence.\n", 180), status: "done"},
		{name: "large_report_unchanged_preview_path", report: strings.Repeat("Distinct complete evidence record.\n", 1000), status: "done"},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker := handoffFixture(test.report, test.evidence, test.status)
			worker.uiModel, worker.uiReasoningEffort = "fixture/model", "high"
			got := prepareWorkerHandoff(worker, test.report).result(worker, test.cause)
			want := legacyHandoffworkerResult(worker, test.report, test.cause)
			if got.Text != want.Text || got.RetainedText != want.RetainedText || (got.Err == nil) != (want.Err == nil) {
				t.Fatal("production optimization changed full report/evidence/error contract")
			}
			if test.cause != nil && (!errors.Is(got.Err, test.cause) || got.Err.Error() != want.Err.Error()) {
				t.Fatal("failure attribution changed")
			}
			if !test.compact {
				if got.ModelText != "" {
					t.Fatal("unsafe or non-saving result was encoded")
				}
				return
			}
			if got.ModelText == "" || len(got.ModelText) >= len(got.Text) || len(got.ModelText) > core.ModelOutputInlineBytes || got.ModelPreview != "" {
				t.Fatal("complete medium report was not compacted within its inline budget")
			}
			expanded, ok := expandWorkerReportModelText(got.ModelText)
			if !ok || expanded != got.Text {
				t.Fatal("production decoder did not preserve every original byte")
			}
			if !strings.Contains(got.ModelText, "EXACT_MIDDLE_FINDING") || !strings.Contains(got.ModelText, "fixture/model") {
				t.Fatal("critical finding or worker metadata omitted")
			}
			// Independent framed decoder also verifies the ordinary no-suffix case.
			if test.evidence == "" && decodeMediumReportExperiment(t, got.ModelText) != got.Text {
				t.Fatal("independent decoder disagrees with production")
			}
			model := core.NewOutputStore().ModelContent("task", got)
			if model != got.ModelText || core.StoredOutputHandle(model) != "" {
				t.Fatal("complete representation added a retrieval handle or changed the model result")
			}
		})
	}
}
