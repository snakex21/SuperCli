package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

// Exercise actual provider requests, not just the preview helper: the paired
// calls, schemas, instructions and saved projection all contribute to context.
func TestWorkerReportRepetitionProviderContext(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, distinct := range []bool{false, true} {
			t.Run(fmt.Sprintf("thin=%t/distinct=%t", thin, distinct), func(t *testing.T) {
				ctx := context.Background()
				report := "# Inspection\nOwner general; worker-27 run 3.\n## Source\ninternal/agent/worker_evidence.go:143\n" + strings.Repeat("Historical observation: the inspected source is unchanged.\n", 600) +
					"EXACT_MIDDLE_EVIDENCE: argv=[\"go\",\"test\",\"./internal/agent\",\"-run\",\"TestEvidence\"]\nDiagnostics: source.go:731:19: missingSymbol; failed check remains unresolved.\nRestriction: do not publish or mark verification passed.\n" + strings.Repeat("Historical observation: no current workspace claim is made.\n", 600) + "## Verification\nNot rerun; keep task1 pending.\n"
				if distinct {
					var body strings.Builder
					body.WriteString("# Inspection\nOwner general; worker-27 run 3.\n## Source\ninternal/agent/worker_evidence.go:143\nRestriction: do not publish or mark verification passed.\n")
					for i := 0; i < 640; i++ {
						if i == 320 {
							body.WriteString("## [P1] Middle failure evidence\nEXACT_MIDDLE_EVIDENCE: argv=[\"go\",\"test\",\"./internal/agent\",\"-run\",\"TestEvidence\"]\nDiagnostics: source.go:731:19: missingSymbol; failed check remains unresolved.\n")
						}
						fmt.Fprintf(&body, "Distinct observation %04d: internal/agent/source_%04d.go:%d has its own inspected value=%d.\n", i, i, i+1, i*17)
					}
					body.WriteString("## Verification\nNot rerun; keep task1 pending.\n")
					report = body.String()
				}
				w := handoffFixture(report, strings.Repeat("== read_lines config.go ==\nHistorical observation.\n", 200), "done")
				current := workerResult(w, report, nil)
				baseline := legacyHandoffworkerResult(w, report, nil)
				if distinct && !reflect.DeepEqual(current, baseline) {
					t.Fatal("pre-existing distinct-report projection changed")
				}
				if current.Text != baseline.Text || current.RetainedText != baseline.RetainedText {
					t.Fatal("full report/observations changed")
				}
				store, err := session.OpenStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				sess, err := store.Create(t.TempDir(), "fixture", "")
				if err != nil {
					t.Fatal(err)
				}
				writer := session.NewWriter(store, sess.ID)
				run := func(result tools.Result, persistent bool) (*Loop, *stubProvider, []Event) {
					t.Helper()
					reg := tools.NewRegistry()
					reg.MustRegister(tools.Tool{Name: "task", Description: "controlled completed worker", Schema: "{}", Fn: func(context.Context, json.RawMessage) (tools.Result, error) { return result, nil }})
					reg.MarkAlwaysOn("task")
					first := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "task-call", Name: "task", Arguments: `{}`}, FinishReason: "tool_calls"}}
					if thin {
						first = []llm.Delta{{Content: "«task»", FinishReason: "stop"}}
					}
					p := &stubProvider{scripts: [][]llm.Delta{first, {{Content: "I will inspect the saved evidence before proceeding.", FinishReason: "stop"}}}}
					cfg := LoopConfig{Provider: p, Registry: reg, ThinTools: thin}
					if persistent {
						cfg.Writer = writer
					}
					l, err := NewLoop(cfg)
					if err != nil {
						t.Fatal(err)
					}
					events := drainEvents(t, mustRun(t, l, "Inspect only; never publish. Keep task1 pending."))
					if len(p.reqs) != 2 {
						t.Fatalf("provider calls=%d", len(p.reqs))
					}
					return l, p, events
				}
				_, before, _ := run(baseline, false)
				_, after, events := run(current, true)
				if distinct {
					if current.ModelPreview == "" || len(current.ModelPreview) > core.ModelOutputPreviewBytes {
						t.Fatal("distinct structured preview missing or unbounded")
					}
					bounded := false
					for _, message := range after.reqs[1] {
						if strings.Contains(message.Content, "Report headings (verbatim; omissions marked):") {
							bounded = len(message.Content) <= core.ModelOutputPreviewBytes+256 && core.StoredOutputHandle(message.Content) != ""
						}
					}
					if !bounded {
						t.Fatal("provider did not receive bounded structured preview plus full-output reference")
					}
				}
				wire := func(p *stubProvider) ([]byte, int) {
					b, err := json.Marshal(struct {
						Messages []llm.Message
						Tools    []llm.ToolDef
					}{p.reqs[1], p.toolDefsReqs[1]})
					if err != nil {
						t.Fatal(err)
					}
					return b, estimateRequestTokens(p.reqs[1], p.toolDefsReqs[1])
				}
				b, bt := wire(before)
				a, at := wire(after)
				if distinct {
					t.Logf("existing distinct-report path: report=%d retained=%d preview=%d bytes; current persisted request=%d bytes/%d estimated tokens", len(report), len(current.RetainedText), len(current.ModelPreview), len(a), at)
				}
				t.Logf("fake-provider request: bytes %d -> %d; estimated tokens %d -> %d; full sha256=%x", len(b), len(a), bt, at, sha256.Sum256([]byte(current.RetainedText)))
				if !distinct && (len(a)*10 >= len(b)*6 || at*10 >= bt*6) {
					t.Errorf("repetition still dominates actual request: bytes %d -> %d; tokens %d -> %d", len(b), len(a), bt, at)
				}
				markers := []string{"do not publish", "task1 pending", "worker-27", "run 3"}
				if distinct {
					markers = append(markers, "## [P1] Middle failure evidence")
				} else {
					markers = append(markers, "EXACT_MIDDLE_EVIDENCE", `argv=`, "missingSymbol", "failed check remains unresolved")
				}
				for _, marker := range markers {
					if !strings.Contains(string(a), marker) {
						t.Errorf("provider lost %q", marker)
					}
				}
				handle := ""
				for _, e := range events {
					if result, ok := e.(ToolResultEvent); ok && result.ID != "" {
						handle = result.OutputHandle
						if result.Output != current.Text {
							t.Fatal("GUI/history result changed")
						}
					}
				}
				if handle == "" {
					t.Fatal("missing full output handle")
				}
				full, err := writer.ReadToolOutput(ctx, handle)
				if err != nil || sha256.Sum256([]byte(full)) != sha256.Sum256([]byte(current.RetainedText)) {
					t.Fatalf("full output changed: %v", err)
				}
				projected, err := store.ReadModelContext(ctx, sess.ID)
				if err != nil {
					t.Fatal(err)
				}
				history, err := store.ReadMessages(ctx, sess.ID)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, m := range history {
					if m.Role == string(llm.RoleTool) && m.Name == "task" {
						found = true
						if !strings.Contains(m.Content, handle) {
							t.Fatal("history lost full report reference")
						}
					}
				}
				if !found {
					t.Fatal("canonical report absent")
				}
				p := &stubProvider{scripts: [][]llm.Delta{{{Content: "Still pending.", FinishReason: "stop"}}}}
				resumed, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), Writer: writer, ThinTools: thin, InitialMessages: projected})
				if err != nil {
					t.Fatal(err)
				}
				drainEvents(t, mustRun(t, resumed, "Continue inspection only."))
				stable := false
				if len(p.reqs) == 1 {
					for i := 0; i+len(projected) <= len(p.reqs[0]); i++ {
						if reflect.DeepEqual(p.reqs[0][i:i+len(projected)], projected) {
							stable = true
							break
						}
					}
				}
				if !stable {
					t.Fatal("resume rewrote historical provider prefix")
				}
				raw, _ := json.Marshal(map[string]any{"handle": handle, "query": "EXACT_MIDDLE_EVIDENCE"})
				got := resumed.invoke(ctx, llm.ToolCall{ID: "read-snapshot", Name: "read_output", Arguments: string(raw)}, make(chan Event, 8))
				if got.failed || len(got.followUps) != 1 || !strings.Contains(got.followUps[0].Content, "EXACT_MIDDLE_EVIDENCE") || !strings.Contains(got.followUps[0].Content, "missingSymbol") || !strings.Contains(got.followUps[0].Content, `argv=["go","test","./internal/agent","-run","TestEvidence"]`) {
					t.Fatal("full evidence unavailable after rehydration")
				}
			})
		}
	}
}

// Independent decoder used by the legacy-byte tests: a counted preview must
// reproduce every original byte, not merely preserve selected marker strings.
func expandCountedWorkerReportPreview(t *testing.T, preview string) string {
	t.Helper()
	const label = "Complete report (adjacent identical lines counted):\n"
	if !strings.Contains(preview, label) {
		t.Fatal("unexpected preview format")
	}
	preview = strings.Replace(preview, label, "", 1)
	var b strings.Builder
	previous := ""
	for preview != "" {
		line, rest, found := strings.Cut(preview, "\n")
		preview = rest
		if found {
			line += "\n"
		}
		if strings.HasPrefix(line, "[previous line repeated ") && strings.HasSuffix(line, " additional times]\n") {
			raw := strings.TrimSuffix(strings.TrimPrefix(line, "[previous line repeated "), " additional times]\n")
			count, err := strconv.Atoi(raw)
			if err != nil || count < 1 || count > 10000 || previous == "" {
				t.Fatal("invalid repetition count")
			}
			b.WriteString(strings.Repeat(previous, count))
		} else {
			b.WriteString(line)
			previous = line
		}
	}
	return b.String()
}

func TestWorkerReportRepetitionCountsExactLines(t *testing.T) {
	for _, report := range []string{
		"α字🙂\nα字🙂\nlast line without newline",
		"same\r\nsame\r\n\n\nrestriction\nrestriction\n",
		"```go\nrun()\nrun()\n```\nnever publish\nnever publish\n",
		"line\nline\nline", // The final non-newline-terminated line is distinct.
	} {
		compact := workerReportCountRepetitions(report, 4096)
		if compact == "" || expandCountedWorkerReportPreview(t, "Complete report (adjacent identical lines counted):\n"+compact) != report {
			t.Fatal("distinct bytes or counts lost")
		}
	}
	report := strings.Repeat("repeat\n", 1000) + strings.Repeat("distinct long body without newlines", 100)
	if workerReportCountRepetitions(report, 128) != "" {
		t.Fatal("incomplete counted report accepted")
	}
}

func TestWorkerReportRepetitionLiteralMarkerFallback(t *testing.T) {
	for _, literal := range []string{
		"[previous line repeated 7 additional times]\n",
		"[previous line repeated 99999999999999999999 additional times]\n",
		"```text\n[previous line repeated 2 additional times]\n```\n",
	} {
		for _, atEnd := range []bool{false, true} {
			report := "# Findings\n## Evidence\n" + literal + strings.Repeat("repeated report line\n", 2000) + "## Checks\nNot run.\n"
			if atEnd {
				report = "# Findings\n## Evidence\n" + strings.Repeat("repeated report line\n", 2000) + "## Checks\n" + literal
			}
			if workerReportCountRepetitions(report, core.ModelOutputPreviewBytes) != "" {
				t.Fatal("literal repetition marker treated as generated encoding")
			}
			w := handoffFixture(report, "", "done")
			if !reflect.DeepEqual(workerResult(w, report, nil), legacyHandoffworkerResult(w, report, nil)) {
				t.Fatal("ambiguous report did not retain the pre-existing preview")
			}
		}
	}
}

func TestWorkerReportRepetitionFallbacks(t *testing.T) {
	for _, report := range []string{"ordinary small report\nordinary small report\n", strings.Repeat("unique long report line without adjacent duplicates. ", 1000)} {
		w := handoffFixture(report, "", "done")
		got, want := workerResult(w, report, nil), legacyHandoffworkerResult(w, report, nil)
		if !reflect.DeepEqual(got, want) {
			t.Fatal("ordinary report changed")
		}
	}
	// Every byte, including diagnostics and the cancellation cause, stays on the
	// existing failure path even when a partial report is highly repetitive.
	w := handoffFixture(strings.Repeat("partial report\n", 2000), "", "stopped")
	got, want := workerResult(w, w.LastResult, context.Canceled), legacyHandoffworkerResult(w, w.LastResult, context.Canceled)
	if got.Err.Error() != want.Err.Error() || got.ModelPreview != "" || got.Text != want.Text {
		t.Fatal("failure/cancellation changed")
	}
}
