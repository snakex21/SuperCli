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
	if result.Output == "" && result.Err == nil {
		return // image-only or empty result; there is no textual evidence to expose
	}
	var b strings.Builder
	fmt.Fprintf(&b, "== %s %s ==\n", call.Name, core.HeadTail(call.Args, 768, 256))
	if result.Err != nil {
		fmt.Fprintf(&b, "ERROR: %s\n", core.HeadTail(result.Err.Error(), 768, 256))
	}
	b.WriteString(core.HeadTail(result.Output, workerEvidenceOutputBytes*3/4, workerEvidenceOutputBytes/4))
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
func workerResult(w *Worker, report string, err error) tools.Result {
	result := tools.Result{Text: renderWorkerNotification(w, report), Err: err}
	w.stateMu.RLock()
	evidence, run, updated, status := w.lastEvidence, w.Runs, w.UpdatedAt, w.Status
	w.stateMu.RUnlock()
	if evidence == "" || status == "running" {
		return result
	}
	observation := fmt.Sprintf(
		"\n\n[Worker %s run %d tool observations at %s; historical snapshots, not current workspace state; outputs may be truncated]\n",
		w.ID, run, updated.UTC().Format("2006-01-02T15:04:05Z")) + evidence
	// Tiny observations cost less than another retrieval round. Larger ones
	// stay off-context; neither case needs another worker inference.
	if len(observation) <= workerEvidenceInlineBytes {
		result.Text += observation
		return result
	}
	result.RetainedText = result.Text + observation
	result.Text += "\n[Attached: worker tool observations (historical snapshots).]"
	return result
}
