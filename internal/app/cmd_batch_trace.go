package app

import (
	"encoding/json"
	"io"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

// batchTraceTool is one `--trace-jsonl` tool line: the eval harness
// matches it as "tool:<name>" against requiredTraceEvents.
type batchTraceTool struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// batchTraceUsage is the final `--trace-jsonl` line of a batch run
// (success or failure). Token fields are the main loop's
// provider-reported totals; requests / ttft count main-purpose model
// calls only, so helper inferences never skew the per-run latency.
type batchTraceUsage struct {
	Type       string  `json:"type"`
	Name       string  `json:"name"`
	Model      string  `json:"model,omitempty"`
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	Cached     int     `json:"cached"`
	CacheWrite int     `json:"cacheWrite"`
	Reasoning  int     `json:"reasoning"`
	Steps      int     `json:"steps"`
	Requests   int     `json:"requests"`
	ToolCalls  int     `json:"toolCalls"`
	TTFTMs     float64 `json:"ttftMs"`
	TTFTAvgMs  float64 `json:"ttftAvgMs"`
	DurationMs int64   `json:"durationMs"`
	Error      bool    `json:"error,omitempty"`
	// Review is set when review_model is on: the token split between
	// the main model and the reviewer, plus each verdict.
	Review *batchTraceReview `json:"review,omitempty"`
}

// batchTrace writes `--trace-jsonl` events as JSON lines. A nil
// *batchTrace is a valid no-op, so the batch loop calls it
// unconditionally and the flag-off output stays byte-identical.
// atLineStart tracks whether stdout is at the start of a line:
// assistant text streams as fragments, so a trace line is preceded
// by a newline when needed and never fuses with model text.
type batchTrace struct {
	w           io.Writer
	atLineStart bool
}

func newBatchTrace(w io.Writer, enabled bool) *batchTrace {
	if !enabled {
		return nil
	}
	return &batchTrace{w: w, atLineStart: true}
}

// noteText records text the caller wrote to the same stream.
func (t *batchTrace) noteText(text string) {
	if t == nil || text == "" {
		return
	}
	t.atLineStart = text[len(text)-1] == '\n'
}

func (t *batchTrace) tool(name string) {
	t.write(batchTraceTool{Type: "tool", Name: name})
}

func (t *batchTrace) usage(line batchTraceUsage) {
	t.write(line)
}

func (t *batchTrace) write(v any) {
	if t == nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	if !t.atLineStart {
		_, _ = io.WriteString(t.w, "\n")
	}
	_, _ = t.w.Write(append(b, '\n'))
	t.atLineStart = true
}

// buildBatchTraceUsage folds the run's usage and the model-call
// ledger into the final usage line. ttftMs is the first main call
// that produced a first delta; ttftAvgMs averages over all such
// main calls.
func buildBatchTraceUsage(model string, u agent.Usage, steps, toolCalls int, calls []stats.Call, wall time.Duration, failed bool) batchTraceUsage {
	line := batchTraceUsage{
		Type: "usage", Name: "run", Model: model,
		Input: u.Input, Output: u.Output, Cached: u.Cached, CacheWrite: u.CacheWrite, Reasoning: u.Reasoning,
		Steps: steps, ToolCalls: toolCalls, DurationMs: wall.Milliseconds(), Error: failed,
	}
	var ttftSumUs int64
	ttftCount := 0
	for _, c := range calls {
		if c.Purpose != llm.PurposeMain {
			continue
		}
		line.Requests++
		if c.TTFTUs <= 0 {
			continue
		}
		if ttftCount == 0 {
			line.TTFTMs = float64(c.TTFTUs) / 1000
		}
		ttftSumUs += c.TTFTUs
		ttftCount++
	}
	if ttftCount > 0 {
		line.TTFTAvgMs = float64(ttftSumUs) / float64(ttftCount) / 1000
	}
	return line
}
