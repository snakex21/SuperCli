package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// A/B harness: the trial now exercises the production handoff. The baseline
// below explicitly removes only ModelText to preserve the old inline behavior.
// No excerpt, extra model call, retrieval handle or user configuration change.
func mediumReportLosslessExperiment(w *Worker, report string, cause error) tools.Result {
	return prepareWorkerHandoff(w, report).result(w, cause)
}

// Decode only the framed report, independently of the production encoder.
// The first report line can share a wire line with <result>; that tag is not
// report content and must never be repeated by a whole-message line decoder.
func decodeMediumReportExperiment(t *testing.T, notification string) string {
	t.Helper()
	const label = "Complete report (adjacent identical lines counted):\n"
	start := strings.Index(notification, "<result>"+label)
	end := strings.LastIndex(notification, "</result>")
	if start < 0 || end < start {
		t.Fatal("missing framed complete report")
	}
	payload := notification[start+len("<result>")+len(label) : end]
	var report strings.Builder
	previous := ""
	for payload != "" {
		line, rest, newline := strings.Cut(payload, "\n")
		payload = rest
		if newline {
			line += "\n"
		}
		if strings.HasPrefix(line, "[previous line repeated ") && strings.HasSuffix(line, " additional times]\n") {
			number := strings.TrimSuffix(strings.TrimPrefix(line, "[previous line repeated "), " additional times]\n")
			count, err := strconv.Atoi(number)
			if err != nil || count < 1 || count > 100000 || previous == "" {
				t.Fatal("invalid repetition count")
			}
			report.WriteString(strings.Repeat(previous, count))
			continue
		}
		report.WriteString(line)
		previous = line
	}
	prefix := notification[:start+len("<result>")]
	// Framing lengths disambiguate raw XML inside a report or evidence suffix.
	markerStart := strings.Index(prefix, workerReportEncodingPrefix)
	if markerStart >= 0 {
		markerEnd := strings.Index(prefix[markerStart:], "</report-encoding>\n")
		if markerEnd < 0 {
			t.Fatal("unterminated model encoding marker")
		}
		lengths := prefix[markerStart+len(workerReportEncodingPrefix) : markerStart+markerEnd]
		encodedLength, expandedLength, ok := strings.Cut(lengths, ":")
		encoded, encodedErr := strconv.Atoi(encodedLength)
		expanded, expandedErr := strconv.Atoi(expandedLength)
		if !ok || encodedErr != nil || expandedErr != nil || encoded != end-(start+len("<result>")+len(label)) || expanded != report.Len() {
			t.Fatal("model encoding lengths do not match the independent round-trip")
		}
		prefix = prefix[:markerStart] + prefix[markerStart+markerEnd+len("</report-encoding>\n"):]
	}
	return prefix + report.String() + notification[end:]
}

func normalizeContextExperimentRequest(t *testing.T, messages []llm.Message) []llm.Message {
	t.Helper()
	out := append([]llm.Message(nil), messages...)
	for index := range out {
		if strings.Contains(out[index].Content, "Complete report (adjacent identical lines counted):\n") {
			out[index].Content = decodeMediumReportExperiment(t, out[index].Content)
		}
		// Separate runs may straddle a wall-clock minute. Ignore only the clock
		// line, never metadata, findings, tool output or any user instruction.
		lines := strings.Split(out[index].Content, "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "Current local date/time:") {
				lines[i] = "<wall-clock stamp>"
			}
		}
		out[index].Content = strings.Join(lines, "\n")
	}
	return out
}

func runMediumReportExperiment(t *testing.T, result tools.Result) (*stubProvider, string) {
	t.Helper()
	registry := tools.NewRegistry()
	registry.MustRegister(tools.Tool{Name: "task", Description: "Isolated completed worker fixture", Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return result, nil }})
	registry.MustRegister(tools.Tool{Name: "inspect_fixture", Description: "Read fixture", ReadOnly: true, Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "Follow-up inspection checked."}, nil
		}})
	registry.MarkAlwaysOn("task")
	registry.MarkAlwaysOn("inspect_fixture")
	provider := &stubProvider{name: "lossless-context-experiment", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "handoff", Name: "task", Arguments: `{}`}, FinishReason: "tool_calls"}},
		{{ToolCall: &llm.ToolCall{ID: "inspect", Name: "inspect_fixture", Arguments: `{}`}, FinishReason: "tool_calls"}},
		{{Content: "Inspection complete; unresolved finding remains visible.", FinishReason: "stop"}},
	}}
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, System: "Stable policy; preserve every finding."})
	if err != nil {
		t.Fatal(err)
	}
	uiOutput := ""
	for _, event := range drainEvents(t, mustRun(t, loop, "Read the worker findings, inspect the fixture, then report.")) {
		switch event := event.(type) {
		case ToolResultEvent:
			if event.ID == "handoff" {
				uiOutput = event.Output
			}
		case ErrorEvent:
			t.Fatal(event.Err)
		}
	}
	return provider, uiOutput
}

func TestMediumWorkerReportLosslessABExperiment(t *testing.T) {
	repeated := strings.Repeat("Inspection status: fixture unchanged.\n", 120) +
		"P2: EXACT_MIDDLE_FINDING remains unresolved; do not claim success.\n" +
		strings.Repeat("Verification status: fixture checked.\n", 30)
	var unique strings.Builder
	for line := 0; line < 80; line++ {
		fmt.Fprintf(&unique, "Finding %04d: distinct fixture evidence and independent condition %04d.\n", line, line)
	}
	for name, report := range map[string]string{
		"medium_repeated": strings.TrimSpace(repeated),
		"unicode_repeated": strings.TrimSpace(strings.Repeat("Kontrola: żółć 日本語 😀 — plik bez zmian.\n", 70) +
			"P2: EXACT_MIDDLE_FINDING remains unresolved; do not claim success.\n" +
			strings.Repeat("Weryfikacja: zażółć gęślą jaźń, wynik zachowany.\n", 20)),
		"medium_unique":  strings.TrimSpace(unique.String()),
		"literal_marker": "User-authored explanation:\n[previous line repeated 3 additional times]\nThis literal prose is not an encoding.",
	} {
		t.Run(name, func(t *testing.T) {
			worker := &Worker{ID: "worker-1", Agent: "review", Status: "done", Runs: 1, uiModel: "fixture-model", uiReasoningEffort: "high"}
			baseline := prepareWorkerHandoff(worker, report).result(worker, nil)
			baseline.ModelText = "" // Independent control: pre-optimization inline result.
			trial := mediumReportLosslessExperiment(worker, report, nil)
			if baseline.Text != trial.Text || baseline.RetainedText != trial.RetainedText || baseline.ModelPreview != trial.ModelPreview || trial.Err != nil {
				t.Fatal("trial changed the complete UI/report contract")
			}
			if strings.HasSuffix(name, "_repeated") {
				if trial.ModelText == "" || decodeMediumReportExperiment(t, trial.ModelText) != baseline.Text {
					t.Fatal("trial did not preserve every original byte")
				}
				if !strings.Contains(trial.ModelText, "EXACT_MIDDLE_FINDING remains unresolved") || !strings.Contains(trial.ModelText, "fixture-model") {
					t.Fatal("trial dropped a critical finding or worker metadata")
				}
			} else if trial.ModelText != "" {
				t.Fatal("unique or marker-shaped prose should remain byte-identical")
			}
			before, beforeUI := runMediumReportExperiment(t, baseline)
			after, afterUI := runMediumReportExperiment(t, trial)
			if len(before.reqs) != 3 || len(after.reqs) != 3 || beforeUI != baseline.Text || afterUI != baseline.Text {
				t.Fatal("trial added calls or changed the complete UI report")
			}
			beforeBytes, afterBytes := 0, 0
			for index := range before.reqs {
				if !reflect.DeepEqual(normalizeContextExperimentRequest(t, before.reqs[index]), normalizeContextExperimentRequest(t, after.reqs[index])) ||
					!reflect.DeepEqual(before.toolDefsReqs[index], after.toolDefsReqs[index]) {
					t.Fatalf("trial changed request %d semantics/schema beyond reversible encoding", index+1)
				}
				beforeBytes += traceNotificationRequest(before.reqs[index]).MessageBytes
				afterBytes += traceNotificationRequest(after.reqs[index]).MessageBytes
			}
			if strings.HasSuffix(name, "_repeated") && afterBytes >= beforeBytes || !strings.HasSuffix(name, "_repeated") && afterBytes != beforeBytes {
				t.Fatal("unexpected context-size change")
			}
			t.Logf("report_bytes=%d baseline_cumulative_message_bytes=%d trial_cumulative_message_bytes=%d reduction=%.1f%% model_calls=%d/%d full_UI_equal=true reversible=true", len(report), beforeBytes, afterBytes, 100*float64(beforeBytes-afterBytes)/float64(beforeBytes), len(before.reqs), len(after.reqs))
		})
	}
}

func TestMediumWorkerReportExperimentLeavesFailuresUntouched(t *testing.T) {
	worker := &Worker{ID: "worker-1", Agent: "review", Status: "failed", Runs: 1}
	report := strings.Repeat("Compilation failed: missingSymbol\n", 120) + "EXACT_FAILURE must remain unresolved."
	cause := errors.New("fixture compilation failed")
	before := prepareWorkerHandoff(worker, report).result(worker, cause)
	after := mediumReportLosslessExperiment(worker, report, cause)
	if before.Text != after.Text || before.ModelPreview != after.ModelPreview || before.RetainedText != after.RetainedText || after.ModelText != "" || after.Err == nil || after.Err.Error() != before.Err.Error() {
		t.Fatal("trial changed failure diagnostics or error status")
	}
}

func TestCLILiveContextIsOneSnapshotNotAccumulatingUserTurns(t *testing.T) {
	registry := tools.NewRegistry()
	registry.MustRegister(tools.Tool{Name: "inspect_fixture", Description: "Read fixture", ReadOnly: true, Schema: `{"type":"object"}`,
		Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
			return tools.Result{Text: "Fixture checked."}, nil
		}})
	registry.MarkAlwaysOn("inspect_fixture")
	provider := &stubProvider{name: "cli-context-snapshot", scripts: [][]llm.Delta{
		{{ToolCall: &llm.ToolCall{ID: "one", Name: "inspect_fixture", Arguments: `{"pass":1}`}, FinishReason: "tool_calls"}},
		{{ToolCall: &llm.ToolCall{ID: "two", Name: "inspect_fixture", Arguments: `{"pass":2}`}, FinishReason: "tool_calls"}},
		{{ToolCall: &llm.ToolCall{ID: "three", Name: "inspect_fixture", Arguments: `{"pass":3}`}, FinishReason: "tool_calls"}},
		{{Content: "Inspection complete.", FinishReason: "stop"}},
	}}
	version, refreshes := "CURRENT_A", 0
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: registry, System: "Stable policy", LiveContextForRun: func(context.Context) (string, error) {
		refreshes++
		return "[memory_briefing]\n" + version + " remembered fixture\n[/memory_briefing]\n[current_goal]\n" + version + " fixture goal\n[/current_goal]", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	drainEvents(t, mustRun(t, loop, "Inspect fixture with three separate reads."))
	prefix := append([]llm.Message(nil), loop.Messages...)
	version = "CURRENT_B"
	drainEvents(t, mustRun(t, loop, "Report the current fixture status."))
	if refreshes != 2 || len(provider.reqs) != 5 {
		t.Fatalf("CLI context caused extra refresh/model calls: refreshes=%d requests=%d", refreshes, len(provider.reqs))
	}
	for index, request := range provider.reqs {
		text := ""
		for _, message := range request {
			text += message.Content
		}
		if strings.Count(text, "[memory_briefing]") != 1 || strings.Count(text, "[current_goal]") != 1 {
			t.Fatalf("request %d duplicates technical snapshots", index+1)
		}
		if index == 4 && (strings.Contains(text, "CURRENT_A") || !strings.Contains(text, "CURRENT_B")) {
			t.Fatal("next run kept an obsolete CLI snapshot")
		}
	}
	if !reflect.DeepEqual(provider.reqs[4][:len(prefix)], prefix) {
		t.Fatal("updating CLI context rewrote the stable history prefix")
	}
	for _, message := range loop.Messages {
		if strings.Contains(message.Content, "[memory_briefing]") || strings.Contains(message.Content, "[current_goal]") {
			t.Fatal("CLI snapshot was appended as a persistent user turn")
		}
	}
	t.Log("two human runs, five model requests, two CLI refreshes; one memory/goal snapshot per request; canonical history and old prefix unchanged")
}
