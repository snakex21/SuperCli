package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type anthropicTerminalTransport func(*http.Request) (*http.Response, error)

func (fn anthropicTerminalTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

// Exposes the complete response, then waits on an explicit completion barrier.
// Tests observe an attempted post-terminal Read without sleeps or polling.
type anthropicTerminalBody struct {
	ctx       context.Context
	data      *strings.Reader
	tailRead  chan struct{}
	release   chan struct{}
	readOnce  sync.Once
	closeOnce sync.Once
}

func (b *anthropicTerminalBody) Read(p []byte) (int, error) {
	if b.data.Len() != 0 {
		return b.data.Read(p)
	}
	b.readOnce.Do(func() { close(b.tailRead) })
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.release:
		return 0, io.EOF
	}
}

func (b *anthropicTerminalBody) Close() error {
	b.closeOnce.Do(func() { close(b.release) })
	return nil
}

func anthropicTerminalWire(chunks ...string) string {
	return "data: " + strings.Join(chunks, "\n\ndata: ") + "\n\n"
}

func anthropicTerminalFullWire() string {
	return anthropicTerminalWire(
		`{"type":"message_start","message":{"usage":{"input_tokens":41,"cache_read_input_tokens":11,"cache_creation_input_tokens":5}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Start. "}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":"","signature":"","extra":"native-field-preserved"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"Full reasoning."}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"signed-reasoning-one"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"unknown_future_event","extra":{"preserve-forward-compatibility":true}}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tool-1","name":"inspect","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"two.go\"}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"content_block_start","index":3,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":3,"delta":{"type":"thinking_delta","thinking":" Final reasoning."}}`,
		`{"type":"content_block_delta","index":3,"delta":{"type":"signature_delta","signature":"signed-reasoning-two"}}`,
		`{"type":"content_block_stop","index":3}`,
		`{"type":"content_block_start","index":4,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":4,"delta":{"type":"text_delta","text":"Complete."}}`,
		`{"type":"content_block_stop","index":4}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":21}}`,
		`{"type":"message_stop"}`,
	)
}

func assertAnthropicTerminalFullResponse(t *testing.T, deltas []Delta) {
	t.Helper()
	var text, reasoning string
	var native *ReasoningBlock
	var tool *ToolCall
	var usage *Usage
	finishCount := 0
	for _, d := range deltas {
		if d.Err != nil {
			t.Fatal(d.Err)
		}
		text += d.Content
		reasoning += d.Reasoning
		if d.NativeReasoning != nil {
			native = d.NativeReasoning
		}
		if d.ToolCall != nil {
			tool = d.ToolCall
		}
		if d.FinishReason != "" {
			finishCount++
			if d.FinishReason != "tool_calls" {
				t.Fatalf("finish=%q", d.FinishReason)
			}
			usage = d.Usage
		}
	}
	if text != "Start. Complete." || reasoning != "Full reasoning. Final reasoning." || native == nil ||
		tool == nil || tool.ID != "tool-1" || tool.Name != "inspect" || tool.Arguments != `{"path":"two.go"}` ||
		usage == nil || usage.Input != 57 || usage.Output != 21 || usage.CachedInput != 11 || usage.Total != 78 || finishCount != 1 {
		t.Fatalf("lost complete response: text=%q reasoning=%q native=%+v tool=%+v usage=%+v finish_count=%d", text, reasoning, native, tool, usage, finishCount)
	}
	var message struct {
		Content []struct {
			Type, Text, Thinking, Signature, Extra string
			Input                                  json.RawMessage
		}
	}
	if err := json.Unmarshal(native.Data, &message); err != nil || len(message.Content) != 5 {
		t.Fatalf("native content=%s err=%v", native.Data, err)
	}
	if message.Content[0].Text != "Start. " || message.Content[1].Signature != "signed-reasoning-one" || message.Content[1].Extra != "native-field-preserved" ||
		string(message.Content[2].Input) != `{"path":"two.go"}` || message.Content[3].Signature != "signed-reasoning-two" || message.Content[4].Text != "Complete." {
		t.Fatalf("native order/signature/tool/text fields damaged: %s", native.Data)
	}
}

func TestAnthropicTerminalCompletePreservesMeteredResponseWithoutEOF(t *testing.T) {
	body := &anthropicTerminalBody{data: strings.NewReader(anthropicTerminalFullWire()), tailRead: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() { _ = body.Close() })
	calls := 0
	transport := anthropicTerminalTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		body.ctx = req.Context()
		return &http.Response{StatusCode: http.StatusOK, Request: req, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}, nil
	})
	p, err := NewAnthropic(AnthropicConfig{Model: "arbitrary-model", BaseURL: "https://transport.invalid", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	stats := make(chan CallStat, 1)
	provider := Metered(p, "arbitrary-transport", PurposeCompact, func(s CallStat) { stats <- s })
	ch, err := provider.Complete(context.Background(), []Message{{Role: RoleUser, Content: "fixture"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan []Delta, 1)
	go func() {
		var deltas []Delta
		for d := range ch {
			deltas = append(deltas, d)
		}
		done <- deltas
	}()
	var deltas []Delta
	readTail := false
	select {
	case deltas = <-done:
	case <-body.tailRead:
		readTail = true
		_ = body.Close()
		deltas = <-done
	}
	assertAnthropicTerminalFullResponse(t, deltas)
	s := <-stats
	if calls != 1 || s.Failed || s.Canceled || s.Purpose != PurposeCompact || s.TokensIn != 57 || s.TokensOut != 21 || s.TokensCached != 11 {
		t.Fatalf("metered result changed: requests=%d stat=%+v", calls, s)
	}
	if readTail {
		t.Fatal("complete message waited for a post-terminal body Read")
	}
}

func TestAnthropicTerminalNormalEOFAndFragmentedResponse(t *testing.T) {
	for _, fragmented := range []bool{false, true} {
		name := "normal"
		var reader io.Reader = strings.NewReader(anthropicTerminalFullWire())
		if fragmented {
			name = "fragmented"
			reader = &hardChunkReader{data: []byte(anthropicTerminalFullWire())}
		}
		t.Run(name, func(t *testing.T) {
			out := make(chan Delta, 32)
			p := &AnthropicProvider{cfg: AnthropicConfig{Model: "arbitrary-model", BaseURL: "https://transport.invalid"}}
			if err := p.streamSSE(context.Background(), reader, out); err != nil {
				t.Fatal(err)
			}
			close(out)
			var deltas []Delta
			for d := range out {
				deltas = append(deltas, d)
			}
			assertAnthropicTerminalFullResponse(t, deltas)
		})
	}
}

func TestAnthropicTerminalDoesNotMaskFailuresBeforeStop(t *testing.T) {
	for _, tc := range []struct{ name, wire, want string }{
		{"missing-stop", anthropicTerminalWire(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`), "before message_stop"},
		{"provider-error", anthropicTerminalWire(`{"type":"error","error":{"message":"overloaded before stop"}}`, `{"type":"message_stop"}`), "overloaded before stop"},
		{"malformed", anthropicTerminalWire(`not valid JSON`, `{"type":"message_stop"}`), "malformed SSE payload"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &AnthropicProvider{}
			out := make(chan Delta, 32)
			err := p.streamSSE(context.Background(), strings.NewReader(tc.wire), out)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want %q", err, tc.want)
			}
			close(out)
			for d := range out {
				if d.FinishReason != "" {
					t.Fatal("failure became a successful terminal response")
				}
			}
		})
	}
}

func TestAnthropicTerminalCancellationDoesNotBecomeSuccess(t *testing.T) {
	for _, wire := range []string{anthropicTerminalFullWire(), anthropicTerminalWire(`{"type":"message_stop"}`)} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		p := &AnthropicProvider{}
		err := p.streamSSE(ctx, strings.NewReader(wire), make(chan Delta))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation=%v", err)
		}
	}
}

func TestAnthropicTerminalPreservesTruncatedFinishReason(t *testing.T) {
	p := &AnthropicProvider{}
	out := make(chan Delta, 4)
	wire := anthropicTerminalWire(`{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":3}}`, `{"type":"message_stop"}`)
	if err := p.streamSSE(context.Background(), strings.NewReader(wire), out); err != nil {
		t.Fatal(err)
	}
	close(out)
	for d := range out {
		if d.FinishReason != "length" || d.Usage == nil || d.Usage.Output != 3 {
			t.Fatalf("truncation changed to success: %+v", d)
		}
	}
}
