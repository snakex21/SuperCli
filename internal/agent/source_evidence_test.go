package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func sourceFixture(file, id string, from, to int) (llm.Message, llm.Message) {
	raw, _ := json.Marshal(map[string]any{"file": file, "from": from, "to": to})
	call := llm.ToolCall{ID: id, Name: "read_lines", Arguments: string(raw)}
	var b strings.Builder
	for line := from; line <= to; line++ {
		fmt.Fprintf(&b, "%4d | const Value%d = %q\n", line, line, strings.Repeat("x", 60))
	}
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}, llm.Message{Role: llm.RoleTool, Name: call.Name, ToolCallID: id, Content: b.String()}
}

func TestSourceProjectionOverlapsPreserveNewCodeAndTranscript(t *testing.T) {
	firstCall, first := sourceFixture("src/a.go", "first", 1, 20)
	secondCall, second := sourceBatchFixture("src/../src/a.go", "second", 8, 24)
	messages := []llm.Message{firstCall, first, secondCall, second}
	view := sourceEvidenceProjection(messages, "")
	if !strings.Contains(view[3].Content, "already shown in tool result first") || !strings.Contains(view[3].Content, "Value24") || strings.Contains(view[3].Content, "Value8 =") {
		t.Fatalf("overlap not reduced safely: %s", view[3].Content)
	}
	if messages[3].Content != second.Content || view[1].Content != first.Content {
		t.Fatal("projection changed canonical/first evidence")
	}
	// Append-only requests must keep the old projected prefix byte-stable.
	thirdCall, third := sourceBatchFixture("src/a.go", "third", 1, 24)
	next := sourceEvidenceProjection(append(messages, thirdCall, third), "")
	for i := range view {
		if view[i].Content != next[i].Content {
			t.Fatal("append rewrote a cached prefix")
		}
	}
	t.Logf("overlapping read: %d -> %d bytes", len(second.Content), len(view[3].Content))
}

func TestSourceProjectionDoesNotReferenceCompactedHiddenOrChangedCode(t *testing.T) {
	a, first := sourceFixture("a.go", "old", 1, 20)
	b, second := sourceBatchFixture("a.go", "new", 1, 20)
	for _, old := range []llm.Message{{Role: llm.RoleSystem, Content: "Summary: a.go was inspected."}, {Role: llm.RoleTool, ToolCallID: "old", Content: pruneMarkerPrefix + ": read_lines; details omitted]"}} {
		view := sourceEvidenceProjection([]llm.Message{a, old, b, second}, "")
		if view[3].Content != second.Content {
			t.Fatal("missing original evidence caused a stub")
		}
	}
	second.Content = strings.ReplaceAll(second.Content, "const Value", "var ChangedValue")
	view := sourceEvidenceProjection([]llm.Message{a, first, b, second}, "")
	if view[3].Content != second.Content {
		t.Fatal("changed bytes were suppressed")
	}
	_, other := sourceBatchFixture("other.go", "different", 1, 20)
	b.ToolCalls[0].Arguments = `{"reads":"other.go:1-20"}`
	other.ToolCallID = b.ToolCalls[0].ID
	view = sourceEvidenceProjection([]llm.Message{a, first, b, other}, "")
	if view[3].Content != other.Content {
		t.Fatal("identical code in another file was suppressed")
	}
}

func TestSourceProjectionBatchSearchDispatcherAndDiagnostics(t *testing.T) {
	a, first := sourceFixture("a.go", "first", 1, 12)
	for _, tool := range []string{"read_many", "search_code", "invoke_tool"} {
		call := llm.ToolCall{ID: "again", Name: tool, Arguments: `{"query":"Value","context":3}`}
		body := "== a.go:1-12 ==\n" + first.Content + "\n[search limit reached; remaining results omitted]"
		if tool == "read_many" {
			body = "== [1] a.go:1-12 ==\n" + first.Content + "== [2] missing.go:1-3 ==\nerror: missing file\n[read_many: 1 ok, 1 failed]"
		}
		if tool == "invoke_tool" {
			call.Arguments = `{"tool":"read_many","args":{"reads":"a.go:1-12"}}`
			body = "== [1] a.go:1-12 ==\n" + first.Content + "[read_many: 1 ok, 0 failed]"
		}
		messages := []llm.Message{a, first, {Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}, {Role: llm.RoleTool, Name: tool, ToolCallID: call.ID, Content: body}}
		view := sourceEvidenceProjection(messages, "")
		if !strings.Contains(view[3].Content, "already shown") || !strings.Contains(view[3].Content, strings.TrimSpace(body[strings.LastIndexByte(body, '\n')+1:])) {
			t.Fatalf("%s framing/diagnostic lost: %s", tool, view[3].Content)
		}
		if tool == "read_many" && !strings.Contains(view[3].Content, "error: missing file") {
			t.Fatal("batch error was hidden")
		}
	}
}

func sourceOutcome(call llm.ToolCall, text string) callOutcome {
	outcome := observed(call, text)
	outcome.source = sourceRows(call, text, "")
	outcome.sourcePartial = sourceResultPartial(call, text, outcome.source)
	return outcome
}

func TestSourceProgressStopsChangingQueriesOverKnownLines(t *testing.T) {
	var progress repeatProgress
	_, first := sourceFixture("a.go", "seed", 1, 20)
	for round := 0; round <= unchangedRoundLimit; round++ {
		call := llm.ToolCall{Name: "search_code", Arguments: fmt.Sprintf(`{"query":"Value|term%d"}`, round)}
		body := "== a.go:1-20 ==\n" + first.Content
		signal := progress.observe([]llm.ToolCall{call}, []callOutcome{sourceOutcome(call, body)})
		if (signal == repeatAbort) != (round == unchangedRoundLimit) {
			t.Fatalf("round=%d signal=%d", round, signal)
		}
		if round == 3 && signal != repeatWarn {
			t.Fatal("changed regex evaded source warning")
		}
	}
	if !strings.Contains(progress.repeatAbortText(), "different queries/ranges") {
		t.Fatal("incorrect stop explanation")
	}
}

func TestSourceProgressAllowsNewFilesRangesChangesAndResetsAfterEdits(t *testing.T) {
	for _, mode := range []string{"files", "ranges", "changes"} {
		var progress repeatProgress
		for round := 0; round < 40; round++ {
			file, from := "a.go", 1
			if mode == "files" {
				file = fmt.Sprintf("f%d.go", round)
			}
			if mode == "ranges" {
				from = round*20 + 1
			}
			a, b := sourceFixture(file, "read", from, from+19)
			if mode == "changes" {
				b.Content = strings.ReplaceAll(b.Content, "const", fmt.Sprintf("version%d", round))
			}
			call := a.ToolCalls[0]
			if signal := progress.observe([]llm.ToolCall{call}, []callOutcome{sourceOutcome(call, b.Content)}); signal != repeatNone {
				t.Fatalf("productive %s warned at %d", mode, round)
			}
		}
	}
	var progress repeatProgress
	a, b := sourceFixture("a.go", "read", 1, 20)
	for i := 0; i < 3; i++ {
		progress.observe(a.ToolCalls, []callOutcome{sourceOutcome(a.ToolCalls[0], b.Content)})
	}
	progress.observe([]llm.ToolCall{{Name: "patch_file"}}, []callOutcome{{}})
	if signal := progress.observe(a.ToolCalls, []callOutcome{sourceOutcome(a.ToolCalls[0], b.Content)}); signal != repeatNone {
		t.Fatal("post-edit verification warned")
	}
}

func TestSourceProgressMostlyRepeatedIsAdviceNotAbort(t *testing.T) {
	var progress repeatProgress
	for round := 0; round < 25; round++ {
		a, b := sourceFixture("a.go", "read", 1, 100)
		b.Content = strings.Replace(b.Content, "const Value100", fmt.Sprintf("const Unique%d", round), 1)
		signal := progress.observe(a.ToolCalls, []callOutcome{sourceOutcome(a.ToolCalls[0], b.Content)})
		if signal == repeatAbort {
			t.Fatal("genuinely new evidence caused abort")
		}
		if round == 8 && !progress.source.warn {
			t.Fatal("mostly repeated discovery had no advice")
		}
	}
}

// Read-only replay of the real bad turn; no provider calls or source mutations.
func TestSourceEvidenceHistoricalReplay(t *testing.T) {
	path := os.Getenv("SUPERCLI_SOURCE_TRACE")
	if path == "" {
		t.Skip("opt-in portable historical trace")
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	var messages []llm.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		t.Fatal(err)
	}
	before, after := 0, 0
	view := sourceEvidenceProjection(messages, "C:/Users/ASRock/Desktop/SuperCli")
	for i := range messages {
		before += len(messages[i].Content)
		after += len(view[i].Content)
	}
	var progress repeatProgress
	firstWarning, steps := 0, 0
	var calls []llm.ToolCall
	var outcomes []callOutcome
	flush := func() {
		if len(calls) == 0 {
			return
		}
		steps++
		if signal := progress.observe(calls, outcomes); signal != repeatNone && firstWarning == 0 {
			firstWarning = steps
		}
	}
	for _, message := range messages {
		if message.Role == llm.RoleAssistant {
			flush()
			calls = message.ToolCalls
			outcomes = nil
		}
		if message.Role == llm.RoleTool {
			for _, call := range calls {
				if call.ID == message.ToolCallID {
					outcomes = append(outcomes, sourceOutcome(call, message.Content))
				}
			}
		}
	}
	flush()
	t.Logf("real turn replay: source result bytes %d -> %d; first guard signal at model step %d of %d", before, after, firstWarning, steps)
	if after >= before || firstWarning == 0 {
		t.Fatal("real changing-query loop escaped improvements")
	}
}

func BenchmarkSourceEvidenceProjection(b *testing.B) {
	var messages []llm.Message
	for i := 0; i < 80; i++ {
		call, result := sourceFixture(fmt.Sprintf("f%d.go", i%8), fmt.Sprintf("read%d", i), 1, 100)
		if i >= 8 {
			call, result = sourceBatchFixture(fmt.Sprintf("f%d.go", i%8), fmt.Sprintf("read%d", i), 1, 100)
		}
		messages = append(messages, call, result)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sourceEvidenceProjection(messages, "")
	}
}
