package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

func TestBatchTraceDisabledIsNoop(t *testing.T) {
	var buf bytes.Buffer
	trace := newBatchTrace(&buf, false)
	trace.noteText("hello")
	trace.tool("read_file")
	trace.usage(batchTraceUsage{Type: "usage", Name: "run"})
	if buf.Len() != 0 {
		t.Fatalf("disabled trace wrote %q", buf.String())
	}
}

func TestBatchTraceLinesNeverFuseWithAssistantText(t *testing.T) {
	var buf bytes.Buffer
	trace := newBatchTrace(&buf, true)
	buf.WriteString("partial assistant text")
	trace.noteText("partial assistant text")
	trace.tool("write_file")
	trace.tool("read_file")
	buf.WriteString("more\n")
	trace.noteText("more\n")
	trace.usage(batchTraceUsage{Type: "usage", Name: "run", Input: 3})
	want := "partial assistant text\n" +
		`{"type":"tool","name":"write_file"}` + "\n" +
		`{"type":"tool","name":"read_file"}` + "\n" +
		"more\n" +
		`{"type":"usage","name":"run","input":3,"output":0,"cached":0,"cacheWrite":0,"reasoning":0,"steps":0,"requests":0,"toolCalls":0,"ttftMs":0,"ttftAvgMs":0,"durationMs":0}` + "\n"
	if buf.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestBuildBatchTraceUsageCountsMainCallsOnly(t *testing.T) {
	calls := []stats.Call{
		{Purpose: "navigator", TTFTUs: 5_000},
		{Purpose: llm.PurposeMain, TTFTUs: 0}, // no first delta: counts as a request, not TTFT
		{Purpose: llm.PurposeMain, TTFTUs: 200_000},
		{Purpose: llm.PurposeMain, TTFTUs: 400_000},
	}
	u := agent.Usage{Input: 1000, Output: 50, Cached: 700, CacheWrite: 200, Reasoning: 9}
	line := buildBatchTraceUsage("m", u, 3, 4, calls, 1500*time.Millisecond, true)
	if line.Requests != 3 || line.TTFTMs != 200 || line.TTFTAvgMs != 300 || line.DurationMs != 1500 {
		t.Fatalf("line = %+v", line)
	}
	if line.Input != 1000 || line.Cached != 700 || line.CacheWrite != 200 || line.Reasoning != 9 || line.Steps != 3 || line.ToolCalls != 4 || !line.Error {
		t.Fatalf("line = %+v", line)
	}
	b, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"error":true`) || !strings.Contains(string(b), `"model":"m"`) {
		t.Fatalf("json = %s", b)
	}
}
