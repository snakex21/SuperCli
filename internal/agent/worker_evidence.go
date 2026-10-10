package agent

import (
	"fmt"
	"strings"

	"supercli/internal/tools"
	"supercli/internal/tools/core"
)

const (
	workerEvidenceInlineBytes = 1024
	workerEvidenceBytes       = 64 * 1024
	workerEvidenceItems       = 32
	workerEvidenceOutputBytes = 8 * 1024
)

// Only tool observations are retained: no worker prompt, reasoning or internal
// conversation. Old entries and long outputs are bounded and explicitly marked.
type workerEvidenceLog struct {
	entries []string
	bytes   int
	omitted int
}

func (e *workerEvidenceLog) add(call ToolCallEvent, result ToolResultEvent) {
	if call.Name == "" || call.Name == "tool_search" || call.Name == "ask_user" {
		return
	}
	if result.Output == "" && result.Err == nil && result.OutputHandle == "" {
		return // image-only or empty result; there is no textual evidence to expose
	}
	var b strings.Builder
	fmt.Fprintf(&b, "== %s %s ==\n", call.Name, core.HeadTail(call.Args, 768, 256))
	if result.Err != nil {
		fmt.Fprintf(&b, "ERROR: %s\n", core.HeadTail(result.Err.Error(), 768, 256))
	}
	b.WriteString(core.HeadTail(result.Output, workerEvidenceOutputBytes*3/4, workerEvidenceOutputBytes/4))
	if result.OutputHandle != "" {
		fmt.Fprintf(&b, "\n[Full tool output: handle=%s; read_output {\"handle\":%q}]", result.OutputHandle, result.OutputHandle)
	}
	entry := b.String()
	e.entries = append(e.entries, entry)
	e.bytes += len(entry)
	for len(e.entries) > workerEvidenceItems || e.bytes > workerEvidenceBytes {
		e.bytes -= len(e.entries[0])
		e.entries[0] = ""
		e.entries = e.entries[1:]
		e.omitted++
	}
}

func (e *workerEvidenceLog) text() string {
	if len(e.entries) == 0 {
		return ""
	}
	var b strings.Builder
	if e.omitted > 0 {
		fmt.Fprintf(&b, "[%d older tool observations omitted]\n", e.omitted)
	}
	b.WriteString(strings.Join(e.entries, "\n\n"))
	return b.String()
}

// workerResult returns tiny observations inline and hands larger ones to the
// parent's existing output store for read_output retrieval. The parent LRU also
// supports attachments without a session writer.
func workerResult(w *Worker, report string, err error) (result tools.Result) {
	return prepareWorkerHandoff(w, report).result(w, err)
}

// A handoff belongs to one completed invocation. Capture its reportable state
// and historical evidence together, then share the immutable wrapper between
// the UI and tool result. Nothing is cached for a later run or continuation.
type workerHandoff struct {
	snapshot     Snapshot
	evidence     string
	report       string
	summary      string
	notification string
}

func prepareWorkerHandoff(w *Worker, report string) workerHandoff {
	w.stateMu.RLock()
	s := Snapshot{
		ID: w.ID, Agent: w.Agent, Description: w.Description, Model: w.Model,
		Status: w.Status, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
		LastError: w.LastError, TokensIn: w.TokensIn, TokensOut: w.TokensOut,
		Steps: w.Steps, ToolNames: append([]string(nil), w.ToolNames...), Runs: w.Runs,
	}
	evidence := w.lastEvidence
	w.stateMu.RUnlock()
	summary := workerSummaryFromSnapshot(s)
	return workerHandoff{
		snapshot: s, evidence: evidence, report: report, summary: summary,
		notification: renderWorkerNotificationFromSnapshot(s, summary, report),
	}
}

func (h workerHandoff) result(w *Worker, err error) (result tools.Result) {
	report := h.report
	notification := h.notification
	result = tools.Result{Text: notification, Err: err}
	var inlineObservation string
	if err != nil {
		defer func() {
			result.Err = workerFailureHandoffFromSnapshot(w, h.snapshot, report, err, inlineObservation)
		}()
	} else {
		defer func() {
			if len(result.Text) > core.ModelOutputInlineBytes {
				result.ModelPreview = workerReportPreviewFromSnapshot(h.snapshot, h.summary, report, result.Text[len(notification):])
			}
		}()
	}
	evidence, run, updated, status := h.evidence, h.snapshot.Runs, h.snapshot.UpdatedAt, h.snapshot.Status
	if evidence == "" || status == "running" {
		return result
	}
	observation := fmt.Sprintf(
		"\n\n[Worker %s run %d tool observations at %s; historical snapshots, not current workspace state; outputs may be truncated]\n",
		h.snapshot.ID, run, updated.UTC().Format("2006-01-02T15:04:05Z")) + evidence
	// Tiny observations cost less than another retrieval round. Larger ones
	// stay off-context; neither case needs another worker inference.
	if len(observation) <= workerEvidenceInlineBytes {
		inlineObservation = observation
		result.Text += observation
		return result
	}
	result.RetainedText = result.Text + observation
	result.Text += "\n[Attached: worker tool observations (historical snapshots).]"
	return result
}
