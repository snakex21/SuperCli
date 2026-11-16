package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// hardChunkReader deliberately breaks the transport at awkward byte
// boundaries. Real TCP/proxy reads are not aligned to SSE frames, JSON or
// UTF-8 code points, so protocol tests must not rely on convenient chunks.
type hardChunkReader struct {
	data []byte
	next int
}

func (r *hardChunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	widths := [...]int{1, 2, 7, 3, 11, 1, 5}
	n := widths[r.next%len(widths)]
	r.next++
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func TestHardProtocolOpenAIParserSurvivesFragmentedSSE(t *testing.T) {
	wire := ": heartbeat\r\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"zażółć\"}}]}\r\n\r\n" +
		"data:{\"choices\":[{\"delta\":{\"content\":\" gęślą\"}}]}\n" +
		"data: [DONE]"
	var payloads []string
	done, err := parseOpenAIDataLines(&hardChunkReader{data: []byte(wire)}, func(data string) error {
		payloads = append(payloads, data)
		return nil
	})
	if err != nil {
		t.Fatalf("fragmented parse: %v", err)
	}
	if !done || len(payloads) != 2 {
		t.Fatalf("done=%v payloads=%d (%q)", done, len(payloads), payloads)
	}
	if !strings.Contains(strings.Join(payloads, ""), "zażółć") {
		t.Fatalf("UTF-8 payload was damaged: %q", payloads)
	}
}

func TestHardProtocolOpenAIParserRejectsOversizedFrame(t *testing.T) {
	// A frame above 1 MiB (a large single-chunk tool call) is accepted; only
	// a line beyond maxSSELineBytes is rejected.
	big := "data: {\"x\":\"" + strings.Repeat("x", 1024*1024+1) + "\"}\n\ndata: [DONE]\n\n"
	var got int
	done, err := parseOpenAIDataLines(strings.NewReader(big), func(data string) error { got = len(data); return nil })
	if err != nil || !done || got < 1024*1024 {
		t.Fatalf("1 MiB+ SSE frame rejected: done=%v err=%v len=%d", done, err, got)
	}
	wire := io.MultiReader(strings.NewReader("data: "), io.LimitReader(hardRepeatReader('x'), maxSSELineBytes+1), strings.NewReader("\n\n"))
	done, err = parseOpenAIDataLines(wire, func(string) error { return nil })
	if err == nil || done || !errors.Is(err, errSSELineTooLong) {
		t.Fatalf("oversized SSE frame accepted: done=%v err=%v", done, err)
	}
}

type hardRepeatReader byte

func (r hardRepeatReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

func TestHardProtocolOpenAICleanEOFClassification(t *testing.T) {
	tests := []struct {
		name       string
		wire       string
		wantFinish string
		wantText   string
		wantTool   *ToolCall
		wantErr    string
	}{
		{
			name:       "content becomes implicit stop",
			wire:       "data: {\"choices\":[{\"delta\":{\"content\":\"visible answer\"}}]}\n\n",
			wantFinish: "stop",
			wantText:   "visible answer",
		},
		{
			name: "fragmented tool call is flushed exactly once",
			wire: "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"ask-1\",\"type\":\"function\",\"function\":{\"name\":\"ask_user\",\"arguments\":\"{\\\"\"}}]}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"x\\\":1}\"}}]}}]}\n\n",
			wantFinish: "tool_calls",
			wantTool:   &ToolCall{ID: "ask-1", Name: "ask_user", Arguments: `{"x":1}`},
		},
		{
			name:    "metadata only is not a successful answer",
			wire:    "data: {\"id\":\"meta-1\",\"object\":\"chat.completion.chunk\",\"choices\":[]}\n\n",
			wantErr: "terminal event",
		},
		{
			name:    "malformed JSON is terminal error",
			wire:    "data: {broken-json}\n\n",
			wantErr: "sse:",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.wire)
			}))
			defer upstream.Close()

			provider, err := NewOpenAI(OpenAIConfig{BaseURL: upstream.URL, Model: "hard-protocol"})
			if err != nil {
				t.Fatal(err)
			}
			stream, err := provider.Complete(context.Background(), []Message{{Role: RoleUser, Content: "test"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			deltas := drainDeltas(t, stream)
			var textOut string
			var tools []ToolCall
			var terminals []Delta
			for _, delta := range deltas {
				textOut += delta.Content
				if delta.ToolCall != nil {
					tools = append(tools, *delta.ToolCall)
				}
				if delta.IsTerminal() {
					terminals = append(terminals, delta)
				}
			}
			if len(terminals) != 1 || !deltas[len(deltas)-1].IsTerminal() {
				t.Fatalf("terminal contract broken: deltas=%s", hardDeltaSummary(deltas))
			}
			terminal := terminals[0]
			if tc.wantErr != "" {
				if terminal.Err == nil || !strings.Contains(terminal.Err.Error(), tc.wantErr) {
					t.Fatalf("terminal error=%v, want substring %q", terminal.Err, tc.wantErr)
				}
				return
			}
			if terminal.Err != nil || terminal.FinishReason != tc.wantFinish || textOut != tc.wantText {
				t.Fatalf("finish=%q err=%v text=%q", terminal.FinishReason, terminal.Err, textOut)
			}
			if tc.wantTool != nil {
				if len(tools) != 1 || tools[0] != *tc.wantTool {
					t.Fatalf("tool calls=%+v, want exactly %+v", tools, *tc.wantTool)
				}
			} else if len(tools) != 0 {
				t.Fatalf("unexpected tools: %+v", tools)
			}
		})
	}
}

func hardDeltaSummary(deltas []Delta) string {
	parts := make([]string, 0, len(deltas))
	for _, delta := range deltas {
		parts = append(parts, fmt.Sprintf("{text:%q tool:%v finish:%q err:%v}", delta.Content, delta.ToolCall, delta.FinishReason, delta.Err))
	}
	return strings.Join(parts, ", ")
}
