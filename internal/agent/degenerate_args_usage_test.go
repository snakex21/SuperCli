package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

const incidentBlock = " RENDERING_ARGS####json 1/1 (1)####{\"\\t\\t:\\t\" } \ufffc RENDERING_ARGS####json 1/1 (1)####{\"\\t\\t,\\t\" } \ufffc"

// A tool call rejected at the output limit is never executed; its (often huge,
// degenerate) arguments must not be stored in history, replayed to the model
// or shown verbatim: a short stub replaces them.
func TestRejectedToolCallArgumentsAreStubbedInHistory(t *testing.T) {
	reg := tools.NewRegistry()
	reg.MustRegister(tools.Tool{Name: "invoke_tool", Description: "dispatch", Schema: `{"type":"object"}`, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
		t.Fatal("rejected call executed")
		return tools.Result{}, nil
	}})
	degenerate := `{"tool":"edit_files","arg.old":"x","arg.new":"y` + strings.Repeat(incidentBlock, 3000)
	truncated := `{"content":"package main\nfunc main() {`
	p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
		{
			{ToolCall: &llm.ToolCall{ID: "deg", Name: "invoke_tool", Arguments: degenerate}},
			{ToolCall: &llm.ToolCall{ID: "cut", Name: "invoke_tool", Arguments: truncated}},
			{FinishReason: "length", Usage: &llm.Usage{Input: 10, Output: 128000, Total: 128010}},
		},
		{{Content: "Retried."}, {FinishReason: "stop", Usage: &llm.Usage{Input: 2, Output: 1, Total: 3}}},
	}}
	writer := &recordingWriter{}
	loop, err := NewLoop(LoopConfig{Provider: p, Registry: reg, Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	events := drainEvents(t, mustRun(t, loop, "rename"))
	for _, ev := range events {
		if call, ok := ev.(ToolCallEvent); ok && strings.Contains(call.Args, "RENDERING_ARGS") {
			t.Fatalf("UI event carries the raw degenerate arguments (%d bytes)", len(call.Args))
		}
	}
	want := map[string]string{
		"deg": `{"_rejected":"degenerate arguments (` + itoa(len(degenerate)) + ` bytes) discarded"}`,
		"cut": `{"_rejected":"truncated arguments (` + itoa(len(truncated)) + ` bytes) discarded"}`,
	}
	check := func(where string, msgs []llm.Message) {
		t.Helper()
		found := 0
		for _, m := range msgs {
			for _, c := range m.ToolCalls {
				if w, ok := want[c.ID]; ok {
					found++
					if c.Arguments != w {
						t.Fatalf("%s: %s args = %.120q, want %s", where, c.ID, c.Arguments, w)
					}
				}
			}
			if strings.Contains(m.Content, "RENDERING_ARGS") {
				t.Fatalf("%s: degenerate block leaked into content", where)
			}
		}
		if found != 2 {
			t.Fatalf("%s: found %d stubbed calls", where, found)
		}
	}
	check("persisted", writer.messages)
	check("next request", p.reqs[1])
	assertTruncationPairs(t, writer.messages, 2)
}

// The usage of an attempt that is retried (here: a runaway tool call stopped
// by the repetition guard, whose provider reported an estimate) is counted,
// not overwritten by the retry's usage.
func TestRetriedAttemptUsageIsCounted(t *testing.T) {
	p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
		{
			{Usage: &llm.Usage{Input: 100, Output: 6000, Total: 6100, Estimated: true}},
			{Err: &llm.RunawayToolCallError{Tool: "invoke_tool", KB: 17, Reason: llm.RunawayRepetition}},
		},
		{{Content: "Done."}, {FinishReason: "stop", Usage: &llm.Usage{Input: 10, Output: 2, Total: 12}}},
	}}
	writer := &recordingWriter{}
	loop, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	var done *DoneEvent
	notice := ""
	for _, ev := range drainEvents(t, mustRun(t, loop, "rename")) {
		switch e := ev.(type) {
		case DoneEvent:
			done = &e
		case NoticeEvent:
			if strings.Contains(e.Text, "runaway") {
				notice = e.Text
			}
		case ErrorEvent:
			t.Fatal(e.Err)
		}
	}
	if done == nil || done.Usage.Input != 110 || done.Usage.Output != 6002 || done.Usage.Total != 6112 {
		t.Fatalf("done=%+v", done)
	}
	if writer.inTok != 110 || writer.outTok != 6002 {
		t.Fatalf("persisted usage=%d/%d", writer.inTok, writer.outTok)
	}
	if !strings.Contains(notice, "runaway invoke_tool call stopped (17 KB of a repeating block)") {
		t.Fatalf("notice=%q", notice)
	}
	note := false
	for _, m := range loop.Messages {
		note = note || strings.Contains(m.Content, "degenerated into a repeating block")
	}
	if !note {
		t.Fatal("no runaway note for the repetition case")
	}
}

// A stream that produced output and then failed without any usage frame
// still counts an estimated usage (input = request estimate, output = the
// streamed transcript).
func TestAbortedStreamWithoutUsageIsEstimated(t *testing.T) {
	boom := errors.New("upstream exploded")
	p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{
		{{Content: strings.Repeat("partial answer text ", 50)}, {Err: boom}},
	}}
	writer := &recordingWriter{}
	loop, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	var failed *ErrorEvent
	for _, ev := range drainEvents(t, mustRun(t, loop, "explain")) {
		if e, ok := ev.(ErrorEvent); ok {
			failed = &e
		}
	}
	if failed == nil || !errors.Is(failed.Err, boom) {
		t.Fatalf("error=%v", failed)
	}
	if failed.Usage.Input <= 0 || failed.Usage.Output < 200 || failed.Usage.Total != failed.Usage.Input+failed.Usage.Output {
		t.Fatalf("aborted stream usage not estimated: %+v", failed.Usage)
	}
	if writer.inTok != failed.Usage.Input || writer.outTok != failed.Usage.Output {
		t.Fatalf("persisted usage=%d/%d", writer.inTok, writer.outTok)
	}
}
