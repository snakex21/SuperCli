package webgui

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// failingToolProvider asks for the same non-existent path twice, then
// stops. Two identical failures are what makes the attempt counter
// observable.
type failingToolProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *failingToolProvider) Name() string { return "failing-tool-script" }

func (p *failingToolProvider) Complete(_ context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.mu.Unlock()

	ch := make(chan llm.Delta, 3)
	if n <= 2 {
		ch <- llm.Delta{Role: llm.RoleAssistant, ToolCall: &llm.ToolCall{
			ID:        "read-" + string(rune('0'+n)),
			Name:      "read_lines",
			Arguments: `{"path":"C:\\definitely\\not\\here\\ghost.md","start":1,"end":5}`,
		}}
		ch <- llm.Delta{FinishReason: "tool_calls"}
	} else {
		ch <- llm.Delta{Role: llm.RoleAssistant, Content: "gave up"}
		ch <- llm.Delta{FinishReason: "stop"}
	}
	close(ch)
	return ch, nil
}

// TestEngine_ToolFailuresReachErrorLog is the regression guard for the
// hole this test was written to close: the web front-end built its
// agent loop without an ErrorLog, so every attributed tool failure was
// classified and then discarded. The log stopped growing the moment
// the GUI became the primary front-end, and nothing failed loudly.
func TestEngine_ToolFailuresReachErrorLog(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewEngine(echoConfig(), dir, dir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	eng.mu.Lock()
	eng.prov = &failingToolProvider{}
	eng.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := eng.runStream(ctx, "read the ghost file", "", "", func(wireEvent) {}); err != nil {
		t.Fatalf("runStream: %v", err)
	}
	// Close flushes and releases the file before we read it back.
	if err := eng.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	recs := readErrorLog(t, filepath.Join(dir, "logs", "tool_errors.log"))
	var got []tools.ErrorRecord
	for _, r := range recs {
		if r.Tool == "read_lines" {
			got = append(got, r)
		}
	}
	if len(got) != 2 {
		t.Fatalf("want 2 read_lines failures in tool_errors.log, got %d (%+v)", len(got), recs)
	}
	// The log must carry enough to answer "did the model repair
	// itself on the next try?": one run, ordered steps, and an
	// attempt counter that escalates when it did not.
	if got[0].RunID == "" || got[0].RunID != got[1].RunID {
		t.Fatalf("run_id must be present and identical within a run: %q vs %q", got[0].RunID, got[1].RunID)
	}
	if got[0].Attempt != 1 || got[1].Attempt != 2 {
		t.Fatalf("attempt must count repeats: got %d then %d", got[0].Attempt, got[1].Attempt)
	}
	if got[1].Step <= got[0].Step {
		t.Fatalf("step must order records within a run: got %d then %d", got[0].Step, got[1].Step)
	}
	if got[0].Category == "" || got[0].Action == "" {
		t.Fatalf("category and action must be recorded: %+v", got[0])
	}
}

func readErrorLog(t *testing.T, path string) []tools.ErrorRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var out []tools.ErrorRecord
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r tools.ErrorRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("bad NDJSON line %q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}
